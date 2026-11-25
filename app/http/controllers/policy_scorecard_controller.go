package controllers

import (
	"fmt"
	"sort"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

type binResponse struct {
	Order    int     `json:"order"`
	MinValue *string `json:"min_value"`
	MaxValue *string `json:"max_value"`
	Points   string  `json:"points"`
	Label    string  `json:"label"`
}

type characteristicResponse struct {
	Order         int           `json:"order"`
	Code          string        `json:"characteristic_code"`
	Variable      string        `json:"variable"`
	Description   string        `json:"description"`
	MissingPoints *string       `json:"missing_points"`
	Bins          []binResponse `json:"bins"`
}

type scoreRange struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

type scorecardBody struct {
	BaseScore       string                   `json:"base_score"`
	ScoreRange      scoreRange               `json:"score_range"`
	Characteristics []characteristicResponse `json:"characteristics"`
}

type scorecardResponse struct {
	PolicyVersionID         string         `json:"policy_version_id"`
	Status                  string         `json:"status"`
	DictionarySchemaVersion int64          `json:"dictionary_schema_version"`
	UpdatedAt               string         `json:"updated_at"`
	Scorecard               *scorecardBody `json:"scorecard"`
}

func toScorecardResponse(v domain.Version, sc *domain.Scorecard) scorecardResponse {
	out := scorecardResponse{
		PolicyVersionID: v.ID, Status: string(v.Status), DictionarySchemaVersion: v.DictionarySchemaVersion,
		UpdatedAt: toVersionSummary(v).UpdatedAt,
	}
	if sc == nil {
		return out
	}
	lo, hi := sc.ScoreRange()
	body := &scorecardBody{
		BaseScore: sc.BaseScore, ScoreRange: scoreRange{Min: lo, Max: hi},
		Characteristics: make([]characteristicResponse, 0, len(sc.Characteristics)),
	}
	for _, c := range sc.Characteristics {
		cr := characteristicResponse{
			Order: c.Order, Code: c.Code, Variable: c.Variable, Description: c.Description,
			MissingPoints: c.MissingPoints, Bins: make([]binResponse, 0, len(c.Bins)),
		}
		for _, b := range c.Bins {
			cr.Bins = append(cr.Bins, binResponse{Order: b.Order, MinValue: b.MinValue, MaxValue: b.MaxValue, Points: b.Points, Label: b.Label})
		}
		body.Characteristics = append(body.Characteristics, cr)
	}
	out.Scorecard = body
	return out
}

// ShowScorecard returns the scorecard of a version; `scorecard` is null when none is configured.
func (c *PolicyController) ShowScorecard(ctx http.Context) http.Response {
	v, sc, err := c.service.GetScorecard(ctx.Context(), ctx.Request().Route("version_id"))
	if err != nil {
		return c.serviceError(ctx, "show scorecard", err)
	}
	return ctx.Response().Json(http.StatusOK, toScorecardResponse(v, sc))
}

// ReplaceScorecard replaces the scorecard of a draft:
// {"base_score", "characteristics": [{"characteristic_code", "variable", "description"?,
// "missing_points"?, "bins": [{"min_value"?, "max_value"?, "points", "label"?}]}]}
func (c *PolicyController) ReplaceScorecard(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"base_score": true, "characteristics": true})
	if resp != nil {
		return resp
	}
	p := &scorecardParser{}
	in := domain.ScorecardInput{BaseScore: p.str(body, "base_score", "base_score")}
	for i, item := range p.list(body, "characteristics", "characteristics", domain.MaxCharacteristics) {
		path := fmt.Sprintf("characteristics[%d]", i)
		obj := p.object(item, path, map[string]bool{
			"characteristic_code": true, "variable": true, "description": true, "missing_points": true, "bins": true,
		})
		if obj == nil {
			in.Characteristics = append(in.Characteristics, domain.CharacteristicInput{})
			continue
		}
		ci := domain.CharacteristicInput{
			Code:          p.str(obj, "characteristic_code", path+".characteristic_code"),
			Variable:      p.str(obj, "variable", path+".variable"),
			Description:   p.str(obj, "description", path+".description"),
			MissingPoints: p.optional(obj, "missing_points", path+".missing_points"),
		}
		for j, raw := range p.list(obj, "bins", path+".bins", domain.MaxBinsPerCharacteristic) {
			binPath := fmt.Sprintf("%s.bins[%d]", path, j)
			b := p.object(raw, binPath, map[string]bool{"min_value": true, "max_value": true, "points": true, "label": true})
			if b == nil {
				ci.Bins = append(ci.Bins, domain.BinInput{})
				continue
			}
			ci.Bins = append(ci.Bins, domain.BinInput{
				MinValue: p.optional(b, "min_value", binPath+".min_value"),
				MaxValue: p.optional(b, "max_value", binPath+".max_value"),
				Points:   p.str(b, "points", binPath+".points"),
				Label:    p.str(b, "label", binPath+".label"),
			})
		}
		in.Characteristics = append(in.Characteristics, ci)
	}
	if len(p.details) > 0 {
		return responses.ValidationError(ctx, "invalid scorecard", p.details...)
	}
	v, sc, err := c.service.ReplaceScorecard(ctx.Context(), ctx.Request().Route("version_id"), in)
	if err != nil {
		return c.serviceError(ctx, "replace scorecard", err)
	}
	return ctx.Response().Json(http.StatusOK, toScorecardResponse(v, sc))
}

// DeleteScorecard removes the scorecard of a draft. It answers 204 also when there was none.
func (c *PolicyController) DeleteScorecard(ctx http.Context) http.Response {
	if err := c.service.RemoveScorecard(ctx.Context(), ctx.Request().Route("version_id")); err != nil {
		return c.serviceError(ctx, "delete scorecard", err)
	}
	return ctx.Response().NoContent()
}

// scorecardParser reads the nested JSON body and collects JSON-shape problems with their paths.
// Numbers are decimal strings, never JSON numbers, so floating point never touches a score.
type scorecardParser struct {
	details []responses.Detail
}

func (p *scorecardParser) add(field, issue string) {
	p.details = append(p.details, responses.Detail{Field: field, Issue: issue})
}

func (p *scorecardParser) str(obj map[string]any, key, path string) string {
	raw, present := obj[key]
	if !present || raw == nil {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		p.add(path, "must_be_string")
		return ""
	}
	return s
}

// optional returns nil when key is absent or null.
func (p *scorecardParser) optional(obj map[string]any, key, path string) *string {
	raw, present := obj[key]
	if !present || raw == nil {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		p.add(path, "must_be_string")
		return nil
	}
	return &s
}

func (p *scorecardParser) list(obj map[string]any, key, path string, max int) []any {
	raw, present := obj[key]
	if !present || raw == nil {
		p.add(path, "required")
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		p.add(path, "must_be_array")
		return nil
	}
	if len(items) > max {
		p.add(path, "too_many")
		return nil
	}
	return items
}

func (p *scorecardParser) object(raw any, path string, allowed map[string]bool) map[string]any {
	obj, ok := raw.(map[string]any)
	if !ok {
		p.add(path, "must_be_object")
		return nil
	}
	var unknown []string
	for key := range obj {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	for _, key := range unknown {
		p.add(path+"."+key, "not_allowed")
	}
	return obj
}
