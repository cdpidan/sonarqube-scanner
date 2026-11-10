package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAnalyzedToday(t *testing.T) {
	// 00:30 in Beijing, still the previous UTC day.
	now := time.Date(2026, 11, 10, 16, 30, 0, 0, time.UTC)
	tests := []struct {
		name, body string
		status     int
		skip, fail bool
	}{
		{"same Beijing day", `{"analyses":[{"date":"2026-11-10T16:01:00+0000"}]}`, 200, true, false},
		{"previous Beijing day", `{"analyses":[{"date":"2026-11-10T15:59:59+0000"}]}`, 200, false, false},
		{"colon offset", `{"analyses":[{"date":"2026-11-11T00:01:00+08:00"}]}`, 200, true, false},
		{"empty history", `{"analyses":[]}`, 200, false, false},
		{"new project", "", 404, false, false},
		{"unauthorized", "secret-response", 401, false, true},
		{"forbidden", "", 403, false, true},
		{"server error", "", 500, false, true},
		{"malformed response", "invalid", 200, false, true},
		{"bad date", `{"analyses":[{"date":"bad"}]}`, 200, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/sonar/api/project_analyses/search" {
					t.Errorf("path: %s", r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("project") != "org:project & name" || q.Get("ps") != "1" || q.Get("branch") != "test/a" {
					t.Errorf("query: %v", q)
				}
				user, pass, ok := r.BasicAuth()
				if !ok || user != "test-token" || pass != "" {
					t.Error("missing authentication")
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			properties := map[string]string{"sonar.host.url": server.URL + "/sonar/", "sonar.projectKey": "org:project & name", "sonar.token": "test-token", "sonar.branch.name": "test/a"}
			skip, err := analyzedToday(server.Client(), properties, now)
			if skip != tt.skip || (err != nil) != tt.fail {
				t.Fatalf("got (%v, %v), want skip=%v error=%v", skip, err, tt.skip, tt.fail)
			}
		})
	}
}

func TestScannerProperties(t *testing.T) {
	workspace := t.TempDir()
	content := "# comment\nsonar.projectKey : file\\:key\\u4e2d\nsonar.host.url=https\\://sonar.example\nsonar.token=unused\nsonar.branch.name=test\\\n  /branch\n"
	if err := os.WriteFile(filepath.Join(workspace, "sonar-project.properties"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	properties, err := scannerProperties([]string{"-Dsonar.projectBaseDir=" + workspace, "-Dsonar.token=override"}, "ignored")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"sonar.projectKey": "file:key中", "sonar.host.url": "https://sonar.example", "sonar.token": "override", "sonar.branch.name": "test/branch"} {
		if properties[key] != want {
			t.Errorf("%s = %q, want %q", key, properties[key], want)
		}
	}
	properties, err = scannerProperties([]string{"-Dsonar.projectKey=explicit"}, t.TempDir())
	if err != nil || properties["sonar.projectKey"] != "explicit" {
		t.Fatalf("missing file fallback: %v %v", properties, err)
	}
	custom := filepath.Join(workspace, "custom.properties")
	if err := os.WriteFile(custom, []byte("sonar.projectKey=custom"), 0600); err != nil {
		t.Fatal(err)
	}
	properties, err = scannerProperties([]string{"-Dproject.settings=" + custom, "-Dsonar.projectKey=override-key"}, workspace)
	if err != nil || properties["sonar.projectKey"] != "override-key" {
		t.Fatalf("custom settings: %v %v", properties, err)
	}
	if _, err := readScannerProperties(strings.NewReader(`sonar.projectKey=\uZZZZ`)); err == nil {
		t.Fatal("expected malformed property error")
	}
}

func TestDailyScanDecision(t *testing.T) {
	now := time.Date(2026, 11, 11, 1, 0, 0, 0, scanDayLocation)
	calls := 0
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(status)
		fmt.Fprint(w, `{"analyses":[{"date":"2026-11-11T00:30:00+0800"}]}`)
	}))
	defer server.Close()
	workspace := t.TempDir()
	args := []string{"-Dsonar.projectKey=project", "-Dsonar.host.url=" + server.URL, "-Dsonar.token=token"}
	p := Plugin{Config: Config{Workspace: workspace, ScanOncePerDay: true}}
	if !p.skipDailyScan(args, now) {
		t.Fatal("expected skip")
	}
	status = http.StatusForbidden
	if p.skipDailyScan(args, now) {
		t.Fatal("API failure must continue scan")
	}
	status = http.StatusOK
	before := calls
	if p.skipDailyScan(append(args, "-Dsonar.pullrequest.key=123"), now) || calls != before {
		t.Fatal("PR must not use daily check")
	}
	t.Setenv("SONARQUBE_SCANNER_PARAMS", `{"sonar.projectKey":"external"}`)
	if p.skipDailyScan(args, now) || calls != before {
		t.Fatal("external parameters must continue scan")
	}
}

func TestExecDailySkipDoesNotRunScannerOrWaitForQualityGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"analyses":[{"date":%q}]}`, time.Now().Format(time.RFC3339))
	}))
	defer server.Close()
	workspace := t.TempDir()
	properties := "sonar.projectKey=file-project\n"
	if err := os.WriteFile(filepath.Join(workspace, "sonar-project.properties"), []byte(properties), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "") // A scanner invocation would fail.
	p := Plugin{Config: Config{Workspace: workspace, Host: server.URL, Token: "token", UseSonarConfigFile: true, ScanOncePerDay: true, WaitQualityGate: true}}
	if err := p.Exec(); err != nil {
		t.Fatal(err)
	}
}
