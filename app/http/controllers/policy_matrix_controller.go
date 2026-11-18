package controllers

import (
	"fmt"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/responses"
	"goravel/app/modules/policy/application"
	"goravel/app/modules/policy/domain"
)

type matrixRowResponse struct {
	Order    int     `json:"order"`
	MinScore *string `json:"min_score"`
	MaxScore *string `json:"max_score"`
	Grade    string  `json:"grade"`
	Decision string  `json:"decision"`
	Label    string  `json:"label"`
}

type matrixBody struct {
	Rows []matrixRowResponse `json:"rows"`
}

type matrixResponse struct {
	PolicyVersionID         string      `json:"policy_version_id"`
	Status                  string      `json:"status"`
	DictionarySchemaVersion int64       `json:"dictionary_schema_version"`
	UpdatedAt               string      `json:"updated_at"`
	ScoreRange              *scoreRange `json:"score_range"`
	CoversScoreRange        *bool       `json:"covers_score_range"`
	DecisionMatrix          *matrixBody `json:"decision_matrix"`
}

func toMatrixResponse(view application.MatrixView) matrixResponse {
	out := matrixResponse{
		PolicyVersionID: view.Version.ID, Status: string(view.Version.Status),
		DictionarySchemaVersion: view.Version.DictionarySchemaVersion,
		UpdatedAt:               toVersionSummary(view.Version).UpdatedAt,
		CoversScoreRange:        view.Covers,
	}
	if view.ScoreRange != nil {
		out.ScoreRange = &scoreRange{Min: view.ScoreRange.Min, Max: view.ScoreRange.Max}
	}
	if len(view.Matrix) > 0 {
		body := &matrixBody{Rows: make([]matrixRowResponse, 0, len(view.Matrix))}
		for _, r := range view.Matrix {
			body.Rows = append(body.Rows, matrixRowResponse{
				Order: r.Order, MinScore: r.MinScore, MaxScore: r.MaxScore, Grade: r.Grade,
				Decision: string(r.Decision), Label: r.Label,
			})
		}
		out.DecisionMatrix = body
	}
	return out
}

// ShowMatrix returns the decision matrix of a version; `decision_matrix` is null when none is
// configured. `score_range` is the scorecard's range the matrix must cover.
func (c *PolicyController) ShowMatrix(ctx http.Context) http.Response {
	view, err := c.service.GetMatrix(ctx.Context(), ctx.Request().Route("version_id"))
	if err != nil {
		return c.serviceError(ctx, "show decision matrix", err)
	}
	return ctx.Response().Json(http.StatusOK, toMatrixResponse(view))
}

// ReplaceMatrix replaces the decision matrix of a draft:
// {"rows": [{"min_score"?, "max_score"?, "grade", "decision", "label"?}, ...]}
func (c *PolicyController) ReplaceMatrix(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"rows": true})
	if resp != nil {
		return resp
	}
	p := &scorecardParser{}
	var rows []domain.MatrixRowInput
	for i, raw := range p.list(body, "rows", "rows", domain.MaxMatrixRows) {
		path := fmt.Sprintf("rows[%d]", i)
		obj := p.object(raw, path, map[string]bool{"min_score": true, "max_score": true, "grade": true, "decision": true, "label": true})
		if obj == nil {
			rows = append(rows, domain.MatrixRowInput{})
			continue
		}
		rows = append(rows, domain.MatrixRowInput{
			MinScore: p.optional(obj, "min_score", path+".min_score"),
			MaxScore: p.optional(obj, "max_score", path+".max_score"),
			Grade:    p.str(obj, "grade", path+".grade"),
			Decision: p.str(obj, "decision", path+".decision"),
			Label:    p.str(obj, "label", path+".label"),
		})
	}
	if len(p.details) > 0 {
		return responses.ValidationError(ctx, "invalid decision matrix", p.details...)
	}
	view, err := c.service.ReplaceMatrix(ctx.Context(), ctx.Request().Route("version_id"), rows)
	if err != nil {
		return c.serviceError(ctx, "replace decision matrix", err)
	}
	return ctx.Response().Json(http.StatusOK, toMatrixResponse(view))
}

// DeleteMatrix removes the decision matrix of a draft. It answers 204 also when there was none.
func (c *PolicyController) DeleteMatrix(ctx http.Context) http.Response {
	if err := c.service.RemoveMatrix(ctx.Context(), ctx.Request().Route("version_id")); err != nil {
		return c.serviceError(ctx, "delete decision matrix", err)
	}
	return ctx.Response().NoContent()
}
