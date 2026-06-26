package audiobooks

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// fakeUserRepo is a minimal access.UserRepository for ABSDownloadPolicy tests.
type fakeUserRepo struct {
	users map[int]*models.User
	err   error
}

func (f *fakeUserRepo) GetByID(_ context.Context, id int) (*models.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	u, ok := f.users[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return u, nil
}

func TestABSDownloadPolicy_ReportsUserPrivilege(t *testing.T) {
	repo := &fakeUserRepo{users: map[int]*models.User{
		1: {ID: 1, DownloadAllowed: true},
		2: {ID: 2, DownloadAllowed: false},
	}}
	p := NewABSDownloadPolicy(repo)

	if got, err := p.DownloadAllowed(context.Background(), "1"); err != nil || !got {
		t.Errorf("user 1: got (%v, %v), want (true, nil)", got, err)
	}
	if got, err := p.DownloadAllowed(context.Background(), "2"); err != nil || got {
		t.Errorf("user 2: got (%v, %v), want (false, nil)", got, err)
	}
}

func TestABSDownloadPolicy_InvalidIDErrors(t *testing.T) {
	p := NewABSDownloadPolicy(&fakeUserRepo{users: map[int]*models.User{}})
	if _, err := p.DownloadAllowed(context.Background(), "not-a-number"); err == nil {
		t.Error("expected error for non-numeric user id")
	}
}

func TestABSDownloadPolicy_LoadErrorPropagates(t *testing.T) {
	p := NewABSDownloadPolicy(&fakeUserRepo{err: errors.New("db down")})
	if _, err := p.DownloadAllowed(context.Background(), "1"); err == nil {
		t.Error("expected load error to propagate")
	}
}

func TestNewABSDownloadPolicy_NilRepo(t *testing.T) {
	if p := NewABSDownloadPolicy(nil); p != nil {
		t.Errorf("expected nil policy for nil repo, got %#v", p)
	}
}
