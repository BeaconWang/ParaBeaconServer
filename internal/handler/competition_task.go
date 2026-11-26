package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/middleware"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/service"
)

const maxCompetitionTaskRequestBytes = service.MaxCompetitionTaskBytes + 1024

type CompetitionTaskService interface {
	Replace(ctx context.Context, userID uuid.UUID, task json.RawMessage) (time.Time, error)
	ListAll(ctx context.Context) ([]model.CompetitionTaskListEntry, error)
}

type CompetitionTaskHandler struct {
	tasks CompetitionTaskService
}

func NewCompetitionTaskHandler(tasks CompetitionTaskService) *CompetitionTaskHandler {
	return &CompetitionTaskHandler{tasks: tasks}
}

func (h *CompetitionTaskHandler) Replace(c *gin.Context) {
	userID, ok := competitionTaskUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCompetitionTaskRequestBytes)
	var request struct {
		Task json.RawMessage `json:"task"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(request.Task) == 0 || string(request.Task) == "null" {
		respondError(c, http.StatusBadRequest, "missing competition task")
		return
	}
	updatedAt, err := h.tasks.Replace(c.Request.Context(), userID, request.Task)
	if errors.Is(err, service.ErrInvalidCompetitionTask) {
		respondError(c, http.StatusBadRequest, "invalid competition task")
		return
	}
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated_at": updatedAt})
}

func (h *CompetitionTaskHandler) List(c *gin.Context) {
	tasks, err := h.tasks.ListAll(c.Request.Context())
	if errors.Is(err, service.ErrInvalidCompetitionTask) {
		respondError(c, http.StatusInternalServerError, "invalid stored competition task")
		return
	}
	if err != nil {
		respondInternalError(c)
		return
	}
	entries := make([]competitionTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		entries = append(entries, competitionTaskResponse{
			Task:        json.RawMessage(task.TaskData),
			UpdatedAt:   task.UpdatedAt,
			Username:    task.Username,
			DisplayName: task.DisplayName,
			OwnerID:     task.UserID,
		})
	}
	c.JSON(http.StatusOK, gin.H{"tasks": entries})
}

type competitionTaskResponse struct {
	Task        json.RawMessage `json:"task"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Username    string          `json:"username"`
	DisplayName string          `json:"display_name"`
	OwnerID     uuid.UUID       `json:"owner_id"`
}

func competitionTaskUserID(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(middleware.AuthenticatedUserIDKey)
	if !ok {
		return uuid.Nil, false
	}
	userID, ok := value.(uuid.UUID)
	return userID, ok && userID != uuid.Nil
}
