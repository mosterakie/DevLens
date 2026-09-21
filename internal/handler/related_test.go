package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mosterakie/DevLens/internal/domain"
	"github.com/mosterakie/DevLens/internal/repository"
)

// TestRelatedIncidentDTOIncludesMatch 确认 match 字段真的透传到 JSON。
//
// 05-related-incidents.md 的诉求是不能让用户看到"错误的相关历史"，
// 所以 UI 必须能区分"确定同类"与"可能同类"。字段名一旦写错或被
// omitempty 吃掉，UI 就只能把两者混在一起展示 —— 这个缺陷在
// 后端单测里不会被发现，只能靠这条 JSON 层面的断言。
func TestRelatedIncidentDTOIncludesMatch(t *testing.T) {
	cases := []struct {
		name string
		in   repository.RelatedIncident
		want string
	}{
		{
			name: "精确命中",
			in: repository.RelatedIncident{
				ID: 1, Title: "精确", Status: domain.StatusOpen,
				CreatedAt: time.Now(), Match: repository.MatchExact,
			},
			want: "exact",
		},
		{
			name: "相似命中",
			in: repository.RelatedIncident{
				ID: 2, Title: "相似", Status: domain.StatusOpen,
				CreatedAt: time.Now(), Match: repository.MatchSimilar, Score: 0.67,
			},
			want: "similar",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dto := relatedIncidentDTO{
				ID:        tt.in.ID,
				Title:     tt.in.Title,
				Severity:  severityPtrFromPtr(tt.in.Severity),
				Status:    tt.in.Status,
				CreatedAt: tt.in.CreatedAt,
				Match:     tt.in.Match,
			}

			b, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}

			var got map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}

			v, ok := got["match"]
			if !ok {
				t.Fatalf("JSON 里没有 match 字段: %s", b)
			}
			if v != tt.want {
				t.Errorf("match = %v, want %q", v, tt.want)
			}
		})
	}
}

// TestRelatedIncidentDTOMatchNeverOmitted 确认 match 不会被 omitempty 省掉。
//
// 用 omitempty 的话，零值（空串）会消失，前端拿到 undefined
// 就无法区分"没有这个字段"与"字段为空"。所以这里显式要求字段恒在。
func TestRelatedIncidentDTOMatchNeverOmitted(t *testing.T) {
	dto := relatedIncidentDTO{ID: 1, Title: "x", Status: domain.StatusOpen, CreatedAt: time.Now()}

	b, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["match"]; !ok {
		t.Errorf("match 字段不该被省略: %s", b)
	}
}

// TestRelatedIncidentDTOHasNoScore 确认相似度得分不外泄到 API。
//
// Score 是内部调试用的（线上出现可疑召回时要知道有多接近阈值），
// 它不是给用户的接口契约。把它放进 DTO 会让前端有理由依赖它，
// 之后想改阈值或换算法就变成破坏性变更。
func TestRelatedIncidentDTOHasNoScore(t *testing.T) {
	dto := relatedIncidentDTO{ID: 1, Title: "x", Status: domain.StatusOpen, CreatedAt: time.Now(), Match: "similar"}

	b, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["score"]; ok {
		t.Errorf("score 不该出现在 API 响应里: %s", b)
	}
}
