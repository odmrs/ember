package source

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fake struct {
	books []Book
	err   error
	wait  time.Duration
}

func (f fake) Search(ctx context.Context, _ string) ([]Book, error) {
	select {
	case <-time.After(f.wait):
		return f.books, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (f fake) Fetch(context.Context, string, string) (Download, error) { return Download{}, nil }
func TestMerge(t *testing.T) {
	g := Book{ID: "g", Title: "Dóm Casmurro", Author: "Machado de Assis", Source: "Project Gutenberg"}
	se := g
	se.ID = "s"
	se.Title = "Dom Casmurro"
	se.Source = "Standard Ebooks"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	books, errs := SearchAll(ctx, []Source{fake{books: []Book{g}}, fake{books: []Book{se}}, fake{err: errors.New("offline")}, fake{wait: time.Hour}}, "dom")
	if len(books) != 1 || books[0].ID != "s" || len(errs) != 2 {
		t.Fatalf("%+v %v", books, errs)
	}
}

func TestAnnaEditionsRemainSelectable(t *testing.T) {
	a := Book{ID: "anna:one", Title: "Same title", Author: "Same author"}
	b := a
	b.ID = "anna:two"
	books, errs := SearchAll(context.Background(), []Source{fake{books: []Book{a, b}}}, "same")
	if len(books) != 2 || len(errs) > 0 {
		t.Fatal(books, errs)
	}
}
