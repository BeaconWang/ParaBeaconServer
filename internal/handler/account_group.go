package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"github.com/parabeacon/server/internal/repository"
	"gorm.io/gorm"
)

const maxAccountGroupRequestBytes = 16 * 1024

type AccountGroupHandler struct {
	repository repository.AccountGroupRepository
}

func NewAccountGroupHandler(repository repository.AccountGroupRepository) *AccountGroupHandler {
	return &AccountGroupHandler{repository: repository}
}

type createGroupRequest struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	PermissionCodes []string `json:"permission_codes"`
}

type updateGroupPermissionsRequest struct {
	PermissionCodes []string `json:"permission_codes"`
}

type setAccountGroupsRequest struct {
	GroupIDs []string `json:"group_ids"`
}

func (h *AccountGroupHandler) List(c *gin.Context) {
	permissions, err := h.repository.ListPermissions(c.Request.Context())
	if err != nil {
		respondInternalError(c)
		return
	}
	groups, err := h.repository.ListGroups(c.Request.Context())
	if err != nil {
		respondInternalError(c)
		return
	}
	accounts, err := h.repository.ListAccounts(c.Request.Context())
	if err != nil {
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"permissions": permissions, "groups": groups, "accounts": accounts})
}

func (h *AccountGroupHandler) Create(c *gin.Context) {
	var request createGroupRequest
	if !decodeAccountGroupJSON(c, &request) {
		return
	}
	name := strings.TrimSpace(request.Name)
	description := strings.TrimSpace(request.Description)
	if name == "" || len([]rune(name)) > 64 || !utf8.ValidString(name) || len([]rune(description)) > 256 || !utf8.ValidString(description) {
		respondError(c, http.StatusBadRequest, "invalid group details")
		return
	}
	group := &model.AccountGroup{Name: name, Description: description}
	if err := h.repository.CreateGroup(c.Request.Context(), group, request.PermissionCodes); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			respondError(c, http.StatusConflict, "group name already exists")
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondError(c, http.StatusBadRequest, "unknown permission")
			return
		}
		respondInternalError(c)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"group": group})
}

func (h *AccountGroupHandler) UpdatePermissions(c *gin.Context) {
	groupID, err := uuid.Parse(strings.TrimSpace(c.Param("group_id")))
	if err != nil || groupID == uuid.Nil {
		respondError(c, http.StatusBadRequest, "invalid group id")
		return
	}
	var request updateGroupPermissionsRequest
	if !decodeAccountGroupJSON(c, &request) {
		return
	}
	if err := h.repository.UpdateGroupPermissions(c.Request.Context(), groupID, request.PermissionCodes); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondError(c, http.StatusNotFound, "group or permission not found")
			return
		}
		respondInternalError(c)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AccountGroupHandler) SetAccountGroups(c *gin.Context) {
	userID, err := uuid.Parse(strings.TrimSpace(c.Param("user_id")))
	if err != nil || userID == uuid.Nil {
		respondError(c, http.StatusBadRequest, "invalid user id")
		return
	}
	var request setAccountGroupsRequest
	if !decodeAccountGroupJSON(c, &request) {
		return
	}
	groupIDs := make([]uuid.UUID, 0, len(request.GroupIDs))
	for _, value := range request.GroupIDs {
		groupID, parseErr := uuid.Parse(strings.TrimSpace(value))
		if parseErr != nil || groupID == uuid.Nil {
			respondError(c, http.StatusBadRequest, "invalid group id")
			return
		}
		groupIDs = append(groupIDs, groupID)
	}
	if err := h.repository.SetAccountGroups(c.Request.Context(), userID, groupIDs); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respondError(c, http.StatusNotFound, "account or group not found")
			return
		}
		respondInternalError(c)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AccountGroupHandler) MyPermissions(c *gin.Context) {
	userID, ok := authenticatedUserID(c)
	if !ok {
		respondError(c, http.StatusUnauthorized, "unauthorized")
		return
	}
	permissions, err := h.repository.ListPermissions(c.Request.Context())
	if err != nil {
		respondInternalError(c)
		return
	}
	granted := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		allowed, checkErr := h.repository.HasPermission(c.Request.Context(), userID, permission.Code)
		if checkErr != nil {
			respondInternalError(c)
			return
		}
		if allowed {
			granted = append(granted, permission.Code)
		}
	}
	c.JSON(http.StatusOK, gin.H{"permissions": granted})
}

func decodeAccountGroupJSON(c *gin.Context, target interface{}) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAccountGroupRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		respondError(c, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}
