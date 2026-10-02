package main

import (
	"testing"

	"helix/internal/metabolism"
)

func TestFindLessonByIDPrefix(t *testing.T) {
	d := metabolism.Delivery{Lessons: []metabolism.DeliveredLesson{
		{ID: "0199a1b2c3d4e5f6a7b8c9d0e1f3"}, {ID: "0199a1b2c3d4e5f6a7b8c9d0e1f4"}, {ID: "01aaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}}
	if l, err := findLesson(d, "01aaaa"); err != nil || l.ID != "01aaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unique prefix: %v %v", l, err)
	}
	if _, err := findLesson(d, "0199a1b2"); err == nil {
		t.Fatal("an ambiguous prefix was accepted")
	}
	if _, err := findLesson(d, "0199a"); err == nil {
		t.Fatal("a prefix under six characters was accepted")
	}
	if _, err := findLesson(d, "ffffffff"); err == nil {
		t.Fatal("an unknown ID was accepted")
	}
}
