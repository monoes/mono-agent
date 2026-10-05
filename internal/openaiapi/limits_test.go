package openaiapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimiterAllowsAtMostNConcurrentTurns(t *testing.T) {
	l := newLimiter(2)
	_, r1, ok1 := l.tryAcquire()
	_, r2, ok2 := l.tryAcquire()
	if !ok1 || !ok2 {
		t.Fatal("the first two acquisitions must succeed")
	}
	if _, _, ok := l.tryAcquire(); ok {
		t.Fatal("a third concurrent turn must be refused")
	}
	r1()
	r1() // releasing twice must not free a second slot
	if _, _, ok := l.tryAcquire(); !ok {
		t.Fatal("a released slot must be reusable")
	}
	if _, _, ok := l.tryAcquire(); ok {
		t.Fatal("a double release freed an extra slot")
	}
	r2()
}

func TestLimiterHandsOutDistinctSlotNumbersAndReusesTheFreedOne(t *testing.T) {
	l := newLimiter(3)
	release := map[int]func(){}
	for range 3 {
		slot, rel, ok := l.tryAcquire()
		if !ok || slot < 0 || slot > 2 || release[slot] != nil {
			t.Fatalf("slot %d (ok=%v): slots must be distinct numbers below the limit", slot, ok)
		}
		release[slot] = rel
	}
	release[1]()
	if slot, _, ok := l.tryAcquire(); !ok || slot != 1 {
		t.Fatalf("the freed slot must be the next one handed out: got %d (ok=%v)", slot, ok)
	}
}

func TestLimiterNeverHasFewerThanOneSlot(t *testing.T) {
	if _, _, ok := newLimiter(0).tryAcquire(); !ok {
		t.Fatal("a limiter built with 0 must still allow one turn")
	}
}

func TestDecodeBody(t *testing.T) {
	decode := func(body string, limit int64) (*apiError, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		var dst map[string]any
		e := decodeBody(httptest.NewRecorder(), req, limit, &dst)
		return e, dst
	}

	if e, dst := decode(`{"a":1}`, 100); e != nil || dst["a"] != float64(1) {
		t.Fatalf("a valid body: %+v, %v", e, dst)
	}
	if e, _ := decode(`{"a":"`+strings.Repeat("x", 200)+`"}`, 50); e == nil || e.Status != http.StatusRequestEntityTooLarge || e.Code != "request_too_large" {
		t.Fatalf("an oversized body: %+v", e)
	}
	if e, _ := decode(`{not json`, 100); e == nil || e.Status != http.StatusBadRequest || e.Code != "invalid_json" {
		t.Fatalf("malformed JSON: %+v", e)
	}
	if e, _ := decode(``, 100); e == nil || e.Status != http.StatusBadRequest {
		t.Fatalf("an empty body: %+v", e)
	}
}
