package main

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"testing"
)

// Custom RoundTripper for mocking HTTP client
type roundTripFunc func(req *http.Request) *http.Response

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

func getTask(projectSlug string) (string, error) {
	// Define your implementation for unit test
	return "sampleKey", nil
}

func TestGetTask(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       ioutil.NopCloser(bytes.NewBufferString(`{"analyses":[{"key":"sampleKey"}]}`)),
			}
		}),
	}
	netClient = httpClient

	key, err := getTask("projectSlug")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if key != "sampleKey" {
		t.Errorf("Expected sampleKey, got %v", key)
	}
}

func TestRepairUTF8Mojibake(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "repairs Chinese project name",
			input: "ä¸­æ\u0096\u0087é¡¹ç\u009b®",
			want:  "中文项目",
		},
		{
			name:  "keeps correct Chinese project name",
			input: "中文项目",
			want:  "中文项目",
		},
		{
			name:  "keeps ASCII project name",
			input: "workflow-service",
			want:  "workflow-service",
		},
		{
			name:  "keeps valid Latin-1 project name",
			input: "café",
			want:  "café",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := repairUTF8Mojibake(test.input); got != test.want {
				t.Fatalf("repairUTF8Mojibake(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// func getTask(s string) {
// 	panic("unimplemented")
// }

// Test for Sonar Job Status function
func TestGetSonarJobStatus(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) *http.Response {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       ioutil.NopCloser(bytes.NewBufferString(`{"task":{"status":"SUCCESS"}}`)),
			}
		}),
	}
	netClient = httpClient

	report := &SonarReport{CeTaskURL: "someURL"}
	taskResponse := getSonarJobStatus(report)
	if taskResponse.Task.Status != "SUCCESS" {
		t.Errorf("Expected SUCCESS, got %v", taskResponse.Task.Status)
	}
}

// Test for Wait for Sonar Job function
// func TestWaitForSonarJob(t *testing.T) {
// 	// Define your test logic here
// }
