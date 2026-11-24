package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	MaxChatImageBytes     = 8 << 20
	MaxChatVoiceBytes     = 12 << 20
	chatMediaMaxDimension = 4096
	chatMediaMaxPixels    = 12_000_000
	chatImageMaxDimension = 1600
)

var ErrInvalidChatMedia = errors.New("invalid chat media")
var ErrChatMediaNotFound = errors.New("chat media not found")

type ChatMediaStorage struct {
	root string
}

func NewChatMediaStorage(root string) (*ChatMediaStorage, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("chat media storage directory is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve chat media storage directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return nil, fmt.Errorf("create chat media storage directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidChatMedia
	}
	if err := os.Chmod(absolute, 0700); err != nil {
		return nil, fmt.Errorf("secure chat media storage directory: %w", err)
	}
	return &ChatMediaStorage{root: absolute}, nil
}

func (s *ChatMediaStorage) Store(ctx context.Context, id uuid.UUID, kind string, source io.Reader) (string, error) {
	if s == nil || id == uuid.Nil || source == nil {
		return "", ErrInvalidChatMedia
	}
	limit := int64(MaxChatImageBytes)
	if kind == ChatKindVoice {
		limit = MaxChatVoiceBytes
	} else if kind != ChatKindImage {
		return "", ErrInvalidChatMedia
	}
	content, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil || len(content) == 0 || int64(len(content)) > limit {
		return "", ErrInvalidChatMedia
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var normalized []byte
	contentType := ""
	switch kind {
	case ChatKindImage:
		normalized, err = normalizeChatImage(content)
		contentType = "image/jpeg"
	case ChatKindVoice:
		if !validChatM4A(content) {
			return "", ErrInvalidChatMedia
		}
		normalized = content
		contentType = "audio/mp4"
	}
	if err != nil {
		return "", ErrInvalidChatMedia
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path := s.mediaPath(id, kind)
	temporary, err := os.CreateTemp(s.root, ".chat-media-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary chat media: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("secure temporary chat media: %w", err)
	}
	if _, err := temporary.Write(normalized); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("write temporary chat media: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", fmt.Errorf("sync temporary chat media: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close temporary chat media: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return "", fmt.Errorf("publish chat media: %w", err)
	}
	return contentType, nil
}

func normalizeChatImage(content []byte) ([]byte, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(content))
	if err != nil || (format != "jpeg" && format != "png") || !validChatImageBounds(config.Width, config.Height) {
		return nil, ErrInvalidChatMedia
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(content))
	if err != nil || decodedFormat != format || !validChatImageBounds(decoded.Bounds().Dx(), decoded.Bounds().Dy()) {
		return nil, ErrInvalidChatMedia
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > chatImageMaxDimension || height > chatImageMaxDimension {
		if width >= height {
			height = height * chatImageMaxDimension / width
			width = chatImageMaxDimension
		} else {
			width = width * chatImageMaxDimension / height
			height = chatImageMaxDimension
		}
	}
	outputImage := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sourceY := bounds.Min.Y + y*bounds.Dy()/height
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			pixel := color.NRGBAModel.Convert(decoded.At(sourceX, sourceY)).(color.NRGBA)
			alpha := uint32(pixel.A)
			outputImage.SetRGBA(x, y, color.RGBA{
				R: uint8((uint32(pixel.R)*alpha + 255*(255-alpha)) / 255),
				G: uint8((uint32(pixel.G)*alpha + 255*(255-alpha)) / 255),
				B: uint8((uint32(pixel.B)*alpha + 255*(255-alpha)) / 255),
				A: 255,
			})
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, outputImage, &jpeg.Options{Quality: 84}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func validChatImageBounds(width, height int) bool {
	return width > 0 && height > 0 && width <= chatMediaMaxDimension && height <= chatMediaMaxDimension && int64(width)*int64(height) <= chatMediaMaxPixels
}

func validChatM4A(content []byte) bool {
	if len(content) < 32 {
		return false
	}
	sawSupportedBrand := false
	sawMovie := false
	sawMediaData := false
	for offset := uint64(0); offset < uint64(len(content)); {
		if uint64(len(content))-offset < 8 {
			return false
		}
		boxSize := uint64(content[offset])<<24 | uint64(content[offset+1])<<16 |
			uint64(content[offset+2])<<8 | uint64(content[offset+3])
		boxType := string(content[offset+4 : offset+8])
		headerSize := uint64(8)
		if boxSize == 1 {
			if uint64(len(content))-offset < 16 {
				return false
			}
			boxSize = uint64(content[offset+8])<<56 | uint64(content[offset+9])<<48 |
				uint64(content[offset+10])<<40 | uint64(content[offset+11])<<32 |
				uint64(content[offset+12])<<24 | uint64(content[offset+13])<<16 |
				uint64(content[offset+14])<<8 | uint64(content[offset+15])
			headerSize = 16
		}
		if boxSize < headerSize || boxSize > uint64(len(content))-offset {
			return false
		}
		boxEnd := offset + boxSize
		switch boxType {
		case "ftyp":
			if offset != 0 || boxSize < 16 || (boxSize-8)%4 != 0 {
				return false
			}
			for brandOffset := offset + 8; brandOffset+4 <= boxEnd; brandOffset += 4 {
				brand := string(content[brandOffset : brandOffset+4])
				if brand == "M4A " || brand == "isom" || brand == "mp42" || brand == "iso2" {
					sawSupportedBrand = true
				}
			}
		case "moov":
			sawMovie = true
		case "mdat":
			sawMediaData = true
		}
		offset = boxEnd
	}
	return sawSupportedBrand && sawMovie && sawMediaData
}

func (s *ChatMediaStorage) Open(id uuid.UUID, kind string) (*os.File, os.FileInfo, error) {
	if s == nil || id == uuid.Nil || (kind != ChatKindImage && kind != ChatKindVoice) {
		return nil, nil, ErrChatMediaNotFound
	}
	filePath := s.mediaPath(id, kind)
	info, err := os.Lstat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, ErrChatMediaNotFound
		}
		return nil, nil, fmt.Errorf("inspect chat media: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxChatVoiceBytes {
		return nil, nil, ErrChatMediaNotFound
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open chat media: %w", err)
	}
	return file, info, nil
}

func (s *ChatMediaStorage) Delete(id uuid.UUID, kind string) error {
	if s == nil || id == uuid.Nil || (kind != ChatKindImage && kind != ChatKindVoice) {
		return ErrInvalidChatMedia
	}
	if err := os.Remove(s.mediaPath(id, kind)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete chat media: %w", err)
	}
	return nil
}

func (s *ChatMediaStorage) mediaPath(id uuid.UUID, kind string) string {
	extension := ".jpg"
	if kind == ChatKindVoice {
		extension = ".m4a"
	}
	return filepath.Join(s.root, id.String()+extension)
}
