package service

import (
	"context"
	"sort"
	"strings"
)

type modelRateLimitCandidateProvider struct {
	admin   AdminService
	gateway *GatewayService
	users   UserRepository
}

func NewModelRateLimitCandidateProvider(admin AdminService, gateway *GatewayService, users UserRepository) ModelRateLimitCandidateProvider {
	return &modelRateLimitCandidateProvider{admin: admin, gateway: gateway, users: users}
}

func (p *modelRateLimitCandidateProvider) ModelRateLimitCandidates(ctx context.Context) ([]string, error) {
	groups, err := p.admin.GetAllGroups(ctx)
	if err != nil {
		return nil, err
	}
	return p.candidatesForGroups(ctx, groups), nil
}

func (p *modelRateLimitCandidateProvider) ModelRateLimitCandidatesForUser(ctx context.Context, userID int64) ([]string, error) {
	user, err := p.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	groups, err := p.admin.GetAllGroups(ctx)
	if err != nil {
		return nil, err
	}
	accessible := groups[:0]
	for i := range groups {
		if user.CanBindGroup(groups[i].ID, groups[i].IsExclusive) {
			accessible = append(accessible, groups[i])
		}
	}
	return p.candidatesForGroups(ctx, accessible), nil
}

func (p *modelRateLimitCandidateProvider) candidatesForGroups(ctx context.Context, groups []Group) []string {
	seen := make(map[string]string)
	for i := range groups {
		group := &groups[i]
		if !group.IsActive() {
			continue
		}
		models := p.modelsForGroup(ctx, group)
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model == "" || strings.Contains(model, "*") {
				continue
			}
			key := strings.ToLower(model)
			if _, ok := seen[key]; !ok {
				seen[key] = model
			}
		}
	}
	result := make([]string, 0, len(seen))
	for _, model := range seen {
		result = append(result, model)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func (p *modelRateLimitCandidateProvider) modelsForGroup(ctx context.Context, group *Group) []string {
	platforms := []string{group.Platform}
	schedulable := map[string]struct{}{}
	if group.Platform == PlatformComposite {
		platforms = []string{PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek}
		schedulable = p.gateway.GetSchedulablePlatforms(ctx, &group.ID)
	}
	var available []string
	for _, platform := range platforms {
		models := p.gateway.GetAvailableModels(ctx, &group.ID, platform)
		if len(models) == 0 && group.Platform != PlatformComposite {
			models = defaultModelsListCandidateIDs(platform)
		} else if len(models) == 0 {
			if _, ok := schedulable[platform]; ok && !IsCNProvider(platform) {
				models = defaultModelsListCandidateIDs(platform)
			}
		}
		available = append(available, models...)
	}
	fallback := defaultModelsListCandidateIDs(group.Platform)
	if !group.CustomModelsListEnabled() {
		return concreteModelCandidates(available, fallback)
	}
	if len(available) == 0 {
		available = fallback
	}
	filtered := make([]string, 0, len(group.ModelsListConfig.Models))
	for _, selected := range group.ModelsListConfig.Models {
		selected = strings.TrimSpace(selected)
		if selected == "" {
			continue
		}
		if strings.Contains(selected, "*") {
			for _, candidate := range concreteModelCandidates(available, fallback) {
				if matchStarPattern(strings.ToLower(selected), strings.ToLower(candidate)) {
					filtered = append(filtered, candidate)
				}
			}
			continue
		}
		for _, allowed := range available {
			allowed = strings.TrimSpace(allowed)
			if strings.EqualFold(allowed, selected) || matchStarPattern(strings.ToLower(allowed), strings.ToLower(selected)) {
				filtered = append(filtered, selected)
				break
			}
		}
	}
	return filtered
}

func concreteModelCandidates(available, fallback []string) []string {
	result := make([]string, 0, len(available))
	for _, model := range available {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if !strings.Contains(model, "*") {
			result = append(result, model)
			continue
		}
		for _, candidate := range fallback {
			if matchStarPattern(strings.ToLower(model), strings.ToLower(candidate)) {
				result = append(result, candidate)
			}
		}
	}
	return result
}
