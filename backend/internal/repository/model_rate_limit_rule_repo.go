package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/modelratelimitrule"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type modelRateLimitRuleRepository struct{ client *ent.Client }

func NewModelRateLimitRuleRepository(client *ent.Client) service.ModelRateLimitRuleStore {
	return &modelRateLimitRuleRepository{client: client}
}

func (r *modelRateLimitRuleRepository) ListModelRateLimitRules(ctx context.Context, userID *int64) ([]service.ModelRateLimitRule, error) {
	query := r.client.ModelRateLimitRule.Query()
	if userID == nil {
		query.Where(modelratelimitrule.UserIDIsNil())
	} else {
		query.Where(modelratelimitrule.UserIDEQ(*userID))
	}
	rows, err := query.Order(ent.Asc(modelratelimitrule.FieldNormalizedPattern)).All(ctx)
	if err != nil {
		return nil, err
	}
	return modelRateLimitRulesFromEnt(rows), nil
}

func (r *modelRateLimitRuleRepository) ReplaceModelRateLimitRules(ctx context.Context, userID *int64, rules []service.ModelRateLimitRule) ([]service.ModelRateLimitRule, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	rollback := func(cause error) error {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%w (rollback: %v)", cause, rollbackErr)
		}
		return cause
	}
	deletion := tx.ModelRateLimitRule.Delete()
	if userID == nil {
		deletion.Where(modelratelimitrule.UserIDIsNil())
	} else {
		deletion.Where(modelratelimitrule.UserIDEQ(*userID))
	}
	if _, err := deletion.Exec(ctx); err != nil {
		return nil, rollback(err)
	}

	created := make([]*ent.ModelRateLimitRule, 0, len(rules))
	if len(rules) > 0 {
		builders := make([]*ent.ModelRateLimitRuleCreate, 0, len(rules))
		for _, rule := range rules {
			builders = append(builders, tx.ModelRateLimitRule.Create().
				SetNillableUserID(userID).
				SetModelPattern(rule.ModelPattern).
				SetNormalizedPattern(rule.NormalizedPattern).
				SetConcurrencyLimit(rule.ConcurrencyLimit).
				SetRpmLimit(rule.RPMLimit).
				SetNillableTpmLimit(rule.TPMLimit))
		}
		created, err = tx.ModelRateLimitRule.CreateBulk(builders...).Save(ctx)
		if err != nil {
			return nil, rollback(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return modelRateLimitRulesFromEnt(created), nil
}

func modelRateLimitRulesFromEnt(rows []*ent.ModelRateLimitRule) []service.ModelRateLimitRule {
	result := make([]service.ModelRateLimitRule, 0, len(rows))
	for _, row := range rows {
		result = append(result, service.ModelRateLimitRule{
			ID: row.ID, UserID: row.UserID, ModelPattern: row.ModelPattern,
			NormalizedPattern: row.NormalizedPattern, ConcurrencyLimit: row.ConcurrencyLimit,
			RPMLimit: row.RpmLimit, TPMLimit: row.TpmLimit,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return result
}
