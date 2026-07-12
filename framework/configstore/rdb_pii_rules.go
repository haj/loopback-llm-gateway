package configstore

import (
	"context"
	"errors"
	"strings"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
)

// PII rule CRUD for the relational config store. Mirrors the guardrail config
// CRUD in rdb.go (GetGuardrailConfigs etc.); the only differences are the table
// type and the extra Type filter.

// GetPIIRules returns a filtered, paginated page of PII rules plus the total
// matching count. Ordered by RuleOrder so the page reflects execution order.
func (s *RDBConfigStore) GetPIIRules(ctx context.Context, params PIIRulesQueryParams) ([]tables.TablePIIRule, int64, error) {
	baseQuery := s.DB().WithContext(ctx).Model(&tables.TablePIIRule{})

	if params.Search != "" {
		search := "%" + strings.ToLower(params.Search) + "%"
		baseQuery = baseQuery.Where("LOWER(name) LIKE ?", search)
	}
	if params.Scope != "" {
		baseQuery = baseQuery.Where("scope = ?", params.Scope)
	}
	if params.ScopeID != "" {
		baseQuery = baseQuery.Where("scope_id = ?", params.ScopeID)
	}
	if params.Type != "" {
		baseQuery = baseQuery.Where("type = ?", params.Type)
	}

	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, err
	}

	limit := params.Limit
	offset := params.Offset
	if limit <= 0 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	var rules []tables.TablePIIRule
	if err := baseQuery.
		Order("rule_order ASC, created_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rules).Error; err != nil {
		return nil, 0, err
	}
	return rules, totalCount, nil
}

// GetPIIRule retrieves a single PII rule by ID.
func (s *RDBConfigStore) GetPIIRule(ctx context.Context, id string) (*tables.TablePIIRule, error) {
	var rule tables.TablePIIRule
	if err := s.DB().WithContext(ctx).First(&rule, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &rule, nil
}

// GetEnabledPIIRules returns every enabled PII rule ordered by execution order.
// Used by the HTTP layer to rebuild the live loopback-guard Redactor / Presidio
// transformer set after a mutation.
func (s *RDBConfigStore) GetEnabledPIIRules(ctx context.Context) ([]tables.TablePIIRule, error) {
	var rules []tables.TablePIIRule
	if err := s.DB().WithContext(ctx).
		Where("enabled = ?", true).
		Order("rule_order ASC, created_at ASC, id ASC").
		Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

// CreatePIIRule inserts a new PII rule.
func (s *RDBConfigStore) CreatePIIRule(ctx context.Context, rule *tables.TablePIIRule, tx ...*gorm.DB) error {
	txDB := s.DB()
	if len(tx) > 0 {
		txDB = tx[0]
	}
	if err := txDB.WithContext(ctx).Create(rule).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// UpdatePIIRule saves an existing PII rule (full overwrite of mutable fields).
// The caller is expected to have loaded the row first.
func (s *RDBConfigStore) UpdatePIIRule(ctx context.Context, rule *tables.TablePIIRule, tx ...*gorm.DB) error {
	txDB := s.DB()
	if len(tx) > 0 {
		txDB = tx[0]
	}
	if err := txDB.WithContext(ctx).Save(rule).Error; err != nil {
		return s.parseGormError(err)
	}
	return nil
}

// DeletePIIRule deletes a PII rule by ID.
func (s *RDBConfigStore) DeletePIIRule(ctx context.Context, id string, tx ...*gorm.DB) error {
	txDB := s.DB()
	if len(tx) > 0 {
		txDB = tx[0]
	}
	var rule tables.TablePIIRule
	if err := txDB.WithContext(ctx).Where("id = ?", id).First(&rule).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	return txDB.WithContext(ctx).Delete(&rule).Error
}
