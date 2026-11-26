package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/parabeacon/server/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AccountGroupRepository interface {
	HasPermission(ctx context.Context, userID uuid.UUID, permission string) (bool, error)
	ListPermissions(ctx context.Context) ([]model.Permission, error)
	ListGroups(ctx context.Context) ([]model.AccountGroupSummary, error)
	CreateGroup(ctx context.Context, group *model.AccountGroup, permissionCodes []string) error
	UpdateGroupPermissions(ctx context.Context, groupID uuid.UUID, permissionCodes []string) error
	ListAccounts(ctx context.Context) ([]model.AccountGroupMember, error)
	SetAccountGroups(ctx context.Context, userID uuid.UUID, groupIDs []uuid.UUID) error
}

type GormAccountGroupRepository struct {
	db *gorm.DB
}

func NewAccountGroupRepository(db *gorm.DB) *GormAccountGroupRepository {
	return &GormAccountGroupRepository{db: db}
}

func (r *GormAccountGroupRepository) HasPermission(ctx context.Context, userID uuid.UUID, permission string) (bool, error) {
	var rootCount int64
	if err := r.db.WithContext(ctx).Table("users").
		Where("id = ? AND LOWER(TRIM(username)) = ?", userID, "beacon").
		Count(&rootCount).Error; err != nil {
		return false, err
	}
	if rootCount > 0 {
		return true, nil
	}

	var count int64
	err := r.db.WithContext(ctx).
		Table("account_group_memberships AS membership").
		Joins("JOIN account_group_permissions AS group_permission ON group_permission.group_id = membership.group_id").
		Where("membership.user_id = ? AND group_permission.permission_code = ?", userID, permission).
		Count(&count).Error
	return count > 0, err
}

func (r *GormAccountGroupRepository) ListPermissions(ctx context.Context) ([]model.Permission, error) {
	var permissions []model.Permission
	err := r.db.WithContext(ctx).Order("category ASC, code ASC").Find(&permissions).Error
	return permissions, err
}

func (r *GormAccountGroupRepository) ListGroups(ctx context.Context) ([]model.AccountGroupSummary, error) {
	var groups []model.AccountGroup
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	result := make([]model.AccountGroupSummary, 0, len(groups))
	for _, group := range groups {
		var permissions []model.Permission
		if err := r.db.WithContext(ctx).
			Table("permissions AS permission").
			Joins("JOIN account_group_permissions AS group_permission ON group_permission.permission_code = permission.code").
			Where("group_permission.group_id = ?", group.ID).
			Order("permission.category ASC, permission.code ASC").
			Find(&permissions).Error; err != nil {
			return nil, err
		}
		var memberCount int64
		if err := r.db.WithContext(ctx).Model(&model.AccountGroupMembership{}).Where("group_id = ?", group.ID).Count(&memberCount).Error; err != nil {
			return nil, err
		}
		result = append(result, model.AccountGroupSummary{AccountGroup: group, Permissions: permissions, MemberCount: memberCount})
	}
	return result, nil
}

func (r *GormAccountGroupRepository) CreateGroup(ctx context.Context, group *model.AccountGroup, permissionCodes []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(group).Error; err != nil {
			return err
		}
		return replaceGroupPermissions(tx, group.ID, permissionCodes)
	})
}

func (r *GormAccountGroupRepository) UpdateGroupPermissions(ctx context.Context, groupID uuid.UUID, permissionCodes []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var group model.AccountGroup
		if err := tx.First(&group, "id = ?", groupID).Error; err != nil {
			return err
		}
		return replaceGroupPermissions(tx, groupID, permissionCodes)
	})
}

func replaceGroupPermissions(tx *gorm.DB, groupID uuid.UUID, permissionCodes []string) error {
	if err := tx.Where("group_id = ?", groupID).Delete(&model.AccountGroupPermission{}).Error; err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(permissionCodes))
	for _, code := range permissionCodes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		var permission model.Permission
		if err := tx.First(&permission, "code = ?", code).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AccountGroupPermission{GroupID: groupID, PermissionCode: code}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *GormAccountGroupRepository) ListAccounts(ctx context.Context) ([]model.AccountGroupMember, error) {
	var users []model.User
	if err := r.db.WithContext(ctx).Select("id, username, email").Order("username ASC").Find(&users).Error; err != nil {
		return nil, err
	}
	members := make([]model.AccountGroupMember, 0, len(users))
	for _, user := range users {
		groupIDs := make([]uuid.UUID, 0)
		if err := r.db.WithContext(ctx).Model(&model.AccountGroupMembership{}).
			Where("user_id = ?", user.ID).Pluck("group_id", &groupIDs).Error; err != nil {
			return nil, err
		}
		members = append(members, model.AccountGroupMember{ID: user.ID, Username: user.Username, Email: user.Email, GroupIDs: groupIDs})
	}
	return members, nil
}

func (r *GormAccountGroupRepository) SetAccountGroups(ctx context.Context, userID uuid.UUID, groupIDs []uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Select("id").First(&user, "id = ?", userID).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&model.AccountGroupMembership{}).Error; err != nil {
			return err
		}
		seen := make(map[uuid.UUID]struct{}, len(groupIDs))
		for _, groupID := range groupIDs {
			if groupID == uuid.Nil {
				continue
			}
			if _, ok := seen[groupID]; ok {
				continue
			}
			seen[groupID] = struct{}{}
			var group model.AccountGroup
			if err := tx.Select("id").First(&group, "id = ?", groupID).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AccountGroupMembership{UserID: userID, GroupID: groupID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func SeedAccountGroups(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		permissions := []model.Permission{
			{Code: model.PermissionAPKManagement, Category: "APK management", DisplayName: "Manage APK releases"},
			{Code: model.PermissionXCRouteCustomization, Category: "XC competition route customization", DisplayName: "Customize XC competition routes"},
			{Code: model.PermissionAccountGroupManagement, Category: "Account administration", DisplayName: "Manage account groups"},
		}
		for _, permission := range permissions {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "code"}}, DoNothing: true}).Create(&permission).Error; err != nil {
				return err
			}
		}
		var group model.AccountGroup
		if err := tx.Where("name = ?", "Beacon administrators").First(&group).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			group = model.AccountGroup{Name: "Beacon administrators", Description: "Built-in administrators for ParaBeacon management"}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoNothing: true}).Create(&group).Error; err != nil {
				return err
			}
			if err := tx.Where("name = ?", "Beacon administrators").First(&group).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		for _, permission := range permissions {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AccountGroupPermission{GroupID: group.ID, PermissionCode: permission.Code}).Error; err != nil {
				return err
			}
		}
		var beaconUsers []model.User
		if err := tx.Where("LOWER(TRIM(username)) = ?", "beacon").Find(&beaconUsers).Error; err != nil {
			return err
		}
		for _, user := range beaconUsers {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.AccountGroupMembership{UserID: user.ID, GroupID: group.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

var ErrInvalidGroupPermission = errors.New("invalid group permission")
