package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gprossliner/xhdl"
	"github.com/stretchr/testify/assert"
	"k8s.io/klog/v2"
)

func TestFailedCommandsReturnHTTP500(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := getCommandHandler("", func(ctx xhdl.Context) {
		klog.FromContext(ctx).Info("testlog entry")
		ctx.Throw(fmt.Errorf("failed"))
	})

	handler.ServeHTTP(rr, req)
	assert.Equal(t, 500, rr.Result().StatusCode)
}

func TestSucceededCommandsReturnHTTP200(t *testing.T) {
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := getCommandHandler("", func(ctx xhdl.Context) {
		klog.FromContext(ctx).Info("testlog entry")
	})

	handler.ServeHTTP(rr, req)
	assert.Equal(t, 200, rr.Result().StatusCode)
}
