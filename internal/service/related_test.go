package service

import (
	"context"
	"errors"
	"testing"

	"github.com/mosterakie/DevLens/internal/repository"
)

// TestRelatedByIncidentIDUsesFingerprint 确认按 ID 查询同类时
// 用的是该记录的指纹，而不是别的什么依据。
func TestRelatedByIncidentIDUsesFingerprint(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	first, err := svc.Submit(ctx, validLog())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Submit(ctx, validLog())
	if err != nil {
		t.Fatal(err)
	}
	// 另一个完全不同的日志，不应出现在同类里。
	if _, err := svc.Submit(ctx, otherLog()); err != nil {
		t.Fatal(err)
	}

	got, err := svc.RelatedByIncidentID(ctx, second.Incident.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 related incident, got %d", len(got))
	}
	if got[0].ID != first.Incident.ID {
		t.Errorf("related id = %d, want %d", got[0].ID, first.Incident.ID)
	}
}

// TestRelatedByIncidentIDExcludesSelf 确认结果不含自身。
func TestRelatedByIncidentIDExcludesSelf(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})
	ctx := context.Background()

	res, err := svc.Submit(ctx, validLog())
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.RelatedByIncidentID(ctx, res.Incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r.ID == res.Incident.ID {
			t.Fatalf("incident %d should not be related to itself", res.Incident.ID)
		}
	}
	if len(got) != 0 {
		t.Errorf("expected no related incidents, got %d", len(got))
	}
}

func TestRelatedByIncidentIDNotFound(t *testing.T) {
	svc := NewIncidentService(newFakeRepo(), &fakeQueue{})
	_, err := svc.RelatedByIncidentID(context.Background(), 4242)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// otherLog 是一段长度合法但内容与 validLog 不同的日志。
func otherLog() string {
	return "panic: runtime error: invalid memory address or nil pointer dereference at 0xc0001 23456 when serving request"
}
