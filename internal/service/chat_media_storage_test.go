package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	chatrepository "github.com/parabeacon/server/internal/repository"
)

func TestChatMediaStorageNormalizesImagesAndRejectsInvalidMedia(t *testing.T) {
	storage, err := NewChatMediaStorage(filepath.Join(t.TempDir(), "chat-media"))
	if err != nil {
		t.Fatalf("NewChatMediaStorage() error = %v", err)
	}
	pngBytes := encodeTestChatPNG(t)
	id := uuid.New()
	contentType, err := storage.Store(context.Background(), id, ChatKindImage, bytes.NewReader(pngBytes))
	if err != nil || contentType != "image/jpeg" {
		t.Fatalf("Store(image) content type = %q, err=%v", contentType, err)
	}
	file, info, err := storage.Open(id, ChatKindImage)
	if err != nil {
		t.Fatalf("Open(image) error = %v", err)
	}
	defer file.Close()
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("stored media permissions = %o, want owner-only", info.Mode().Perm())
	}
	if _, err := jpeg.Decode(file); err != nil {
		t.Fatalf("stored image is not a normalized JPEG: %v", err)
	}
	if _, err := storage.Store(context.Background(), uuid.New(), ChatKindImage, bytes.NewReader([]byte("not an image"))); err == nil {
		t.Fatal("Store accepted non-image bytes")
	}
	if _, err := storage.Store(context.Background(), uuid.New(), ChatKindVoice, bytes.NewReader([]byte("not audio"))); err == nil {
		t.Fatal("Store accepted non-audio bytes")
	}
}

func TestChatMediaStorageValidatesM4AContainerAndGeneratedPath(t *testing.T) {
	storage, err := NewChatMediaStorage(filepath.Join(t.TempDir(), "chat-media"))
	if err != nil {
		t.Fatalf("NewChatMediaStorage() error = %v", err)
	}
	id := uuid.New()
	m4a := testM4A()
	contentType, err := storage.Store(context.Background(), id, ChatKindVoice, bytes.NewReader(m4a))
	if err != nil || contentType != "audio/mp4" {
		t.Fatalf("Store(voice) content type = %q, err=%v", contentType, err)
	}
	file, info, err := storage.Open(id, ChatKindVoice)
	if err != nil {
		t.Fatalf("Open(voice) error = %v", err)
	}
	if info.Size() != int64(len(m4a)) {
		t.Fatalf("stored voice size = %d, want %d", info.Size(), len(m4a))
	}
	if _, _, err := storage.Open(uuid.New(), ChatKindVoice); err == nil {
		t.Fatal("Open returned an unknown media file")
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(voice) error = %v", err)
	}
	if err := storage.Delete(id, ChatKindVoice); err != nil {
		t.Fatalf("Delete(voice) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(storage.root, id.String()+".m4a")); !os.IsNotExist(err) {
		t.Fatalf("deleted voice file stat error = %v, want not-exist", err)
	}
}

func TestChatMediaUploadIsBoundToTheAuthorizedDirectConversation(t *testing.T) {
	aliceID, bobID, outsiderID := uuid.New(), uuid.New(), uuid.New()
	repository := &memoryChatRepository{users: map[uuid.UUID]chatrepository.ChatUser{
		aliceID:    {ID: aliceID, PilotNumber: 123456, ChannelNumber: 345678, Username: "alice"},
		bobID:      {ID: bobID, PilotNumber: 234567, ChannelNumber: 456789, Username: "bob"},
		outsiderID: {ID: outsiderID, PilotNumber: 345678, ChannelNumber: 456789, Username: "outsider"},
	}}
	chat := NewChatService(repository)
	storage, err := NewChatMediaStorage(filepath.Join(t.TempDir(), "chat-media"))
	if err != nil {
		t.Fatalf("NewChatMediaStorage() error = %v", err)
	}
	mediaService := NewChatMediaService(repository, chat, storage)
	mediaID, err := mediaService.Upload(
		context.Background(), aliceID, ChatScopeDirect, 0, 234567,
		ChatKindImage, bytes.NewReader(encodeTestChatPNG(t)),
	)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if _, _, _, err := mediaService.Open(context.Background(), bobID, mediaID); err == nil {
		t.Fatal("staged media became readable before a chat message was created")
	}
	message, err := chat.Send(context.Background(), aliceID, SendChatRequest{
		Scope: ChatScopeDirect, PilotNumber: 234567, Kind: ChatKindImage, MediaID: &mediaID,
	})
	if err != nil || message.MediaID == nil || *message.MediaID != mediaID {
		t.Fatalf("Send(media) = %#v, err=%v", message, err)
	}
	file, _, _, err := mediaService.Open(context.Background(), bobID, mediaID)
	if err != nil {
		t.Fatalf("recipient cannot open attached media: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close recipient media: %v", err)
	}
	if _, _, _, err := mediaService.Open(context.Background(), outsiderID, mediaID); err == nil {
		t.Fatal("unrelated pilot can read a direct-message image")
	}
}

func TestChatChannelMediaIsOnlyReadableByCurrentChannelMembers(t *testing.T) {
	aliceID, sameChannelID, otherChannelID := uuid.New(), uuid.New(), uuid.New()
	const channelNumber = 345678
	repository := &memoryChatRepository{users: map[uuid.UUID]chatrepository.ChatUser{
		aliceID:       {ID: aliceID, PilotNumber: 123456, ChannelNumber: channelNumber, Username: "alice"},
		sameChannelID: {ID: sameChannelID, PilotNumber: 234567, ChannelNumber: channelNumber, Username: "same_channel"},
		otherChannelID: {
			ID: otherChannelID, PilotNumber: 345678, ChannelNumber: 987654, Username: "other_channel",
		},
	}}
	chat := NewChatService(repository)
	storage, err := NewChatMediaStorage(filepath.Join(t.TempDir(), "chat-media"))
	if err != nil {
		t.Fatalf("NewChatMediaStorage() error = %v", err)
	}
	mediaService := NewChatMediaService(repository, chat, storage)
	mediaID, err := mediaService.Upload(
		context.Background(), aliceID, ChatScopeChannel, channelNumber, 0,
		ChatKindImage, bytes.NewReader(encodeTestChatPNG(t)),
	)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	message, err := chat.Send(context.Background(), aliceID, SendChatRequest{
		Scope: ChatScopeChannel, ChannelNumber: channelNumber,
		Kind: ChatKindImage, MediaID: &mediaID,
	})
	if err != nil || message.MediaID == nil {
		t.Fatalf("Send(channel media) = %#v, err=%v", message, err)
	}
	file, _, _, err := mediaService.Open(context.Background(), sameChannelID, mediaID)
	if err != nil {
		t.Fatalf("current channel member cannot open image: %v", err)
	}
	_ = file.Close()
	if _, _, _, err := mediaService.Open(context.Background(), otherChannelID, mediaID); err == nil {
		t.Fatal("pilot outside the channel can read a channel image")
	}
}

func encodeTestChatPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var content bytes.Buffer
	if err := png.Encode(&content, img); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	return content.Bytes()
}

func testM4A() []byte {
	ftyp := append([]byte("M4A "), 0, 0, 0, 0)
	ftyp = append(ftyp, []byte("M4A mp42")...)
	content := testMP4Box("ftyp", ftyp)
	content = append(content, testMP4Box("mdat", []byte{1, 2, 3, 4})...)
	return append(content, testMP4Box("moov", []byte{0, 0, 0, 0})...)
}

func testMP4Box(kind string, content []byte) []byte {
	box := make([]byte, 8+len(content))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], kind)
	copy(box[8:], content)
	return box
}
