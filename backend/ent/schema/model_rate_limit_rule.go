package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
)

// ModelRateLimitRule stores global defaults and per-user replacement rules.
type ModelRateLimitRule struct{ ent.Schema }

func (ModelRateLimitRule) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "model_rate_limit_rules"}}
}

func (ModelRateLimitRule) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (ModelRateLimitRule) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id").Optional().Nillable(),
		field.String("model_pattern").MaxLen(255).NotEmpty(),
		field.String("normalized_pattern").MaxLen(255).NotEmpty(),
		field.Int("concurrency_limit").Default(0).NonNegative(),
		field.Int("rpm_limit").Default(0).NonNegative(),
		field.Int("tpm_limit").Optional().Nillable().NonNegative(),
	}
}

func (ModelRateLimitRule) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("model_rate_limit_rules").Field("user_id").Unique(),
	}
}

func (ModelRateLimitRule) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id"), index.Fields("normalized_pattern")}
}
