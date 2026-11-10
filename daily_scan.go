package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

var scanDayLocation = time.FixedZone("UTC+08:00", 8*60*60)

func (p Plugin) skipDailyScan(args []string, now time.Time) bool {
	properties, err := scannerProperties(args, p.Config.Workspace)
	if err != nil {
		fmt.Println("Daily scan check: unable to read scanner properties; continuing scan.")
		return false
	}
	// A pull request needs its own analysis, regardless of today's branch analysis.
	if properties["sonar.pullrequest.key"] != "" || os.Getenv("SONARQUBE_SCANNER_PARAMS") != "" {
		fmt.Println("Daily scan check: PR or externally supplied scanner parameters; continuing scan.")
		return false
	}
	skip, err := analyzedToday(&http.Client{Timeout: 10 * time.Second}, properties, now)
	if err != nil {
		fmt.Printf("Daily scan check: %v; continuing scan.\n", err)
		return false
	}
	if skip {
		fmt.Printf("Skipping scan: project %q already has an analysis on %s (UTC+08:00).\n", properties["sonar.projectKey"], now.In(scanDayLocation).Format("2006-01-02"))
	} else {
		fmt.Println("Daily scan check: no analysis today; continuing scan.")
	}
	return skip
}

func analyzedToday(client *http.Client, properties map[string]string, now time.Time) (bool, error) {
	host, key, token := properties["sonar.host.url"], properties["sonar.projectKey"], properties["sonar.token"]
	if token == "" {
		token = properties["sonar.login"]
	}
	if host == "" {
		host = os.Getenv("SONAR_HOST_URL")
	}
	if token == "" {
		token = os.Getenv("SONAR_TOKEN")
	}
	if host == "" || key == "" || token == "" {
		return false, fmt.Errorf("missing SonarQube host, project key or token")
	}
	endpoint, err := url.Parse(strings.TrimRight(host, "/") + "/api/project_analyses/search")
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return false, fmt.Errorf("invalid SonarQube host")
	}
	query := endpoint.Query()
	query.Set("project", key)
	query.Set("ps", "1")
	if branch := properties["sonar.branch.name"]; branch != "" {
		query.Set("branch", branch)
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return false, fmt.Errorf("unable to create analysis history request")
	}
	// Match the plugin's existing token-as-username authentication.
	req.SetBasicAuth(token, "")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("analysis history request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("analysis history returned HTTP %d", resp.StatusCode)
	}
	var history struct {
		Analyses []struct {
			Date string `json:"date"`
		} `json:"analyses"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&history); err != nil {
		return false, fmt.Errorf("invalid analysis history response")
	}
	if len(history.Analyses) == 0 {
		return false, nil
	}
	var date time.Time
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700"} {
		date, err = time.Parse(layout, history.Analyses[0].Date)
		if err == nil {
			break
		}
	}
	if err != nil {
		return false, fmt.Errorf("invalid analysis date")
	}
	return date.In(scanDayLocation).Format("2006-01-02") == now.In(scanDayLocation).Format("2006-01-02"), nil
}

// Resolve the same properties file and -D overrides that SonarScanner receives.
func scannerProperties(args []string, workspace string) (map[string]string, error) {
	overrides := make(map[string]string)
	for _, arg := range args {
		if strings.HasPrefix(arg, "-D") {
			pair := strings.SplitN(strings.TrimPrefix(arg, "-D"), "=", 2)
			if len(pair) == 2 {
				overrides[pair[0]] = pair[1]
			}
		}
	}
	if base := overrides["sonar.projectBaseDir"]; base != "" {
		workspace = base
	}
	configFile := overrides["project.settings"]
	if configFile == "" {
		configFile = "sonar-project.properties"
	}
	if !filepath.IsAbs(configFile) {
		configFile = filepath.Join(workspace, configFile)
	}
	properties := make(map[string]string)
	file, err := os.Open(configFile)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		defer file.Close()
		properties, err = readScannerProperties(file)
		if err != nil {
			return nil, err
		}
	}
	for key, value := range overrides {
		properties[key] = value
	}
	return properties, nil
}

// Java properties support escaped separators, Unicode and continued lines.
func readScannerProperties(reader io.Reader) (map[string]string, error) {
	properties := make(map[string]string)
	scanner := bufio.NewScanner(reader)
	var line string
	continuing := false
	for scanner.Scan() {
		part := strings.TrimLeft(scanner.Text(), " \t\f")
		if !continuing && (part == "" || strings.HasPrefix(part, "#") || strings.HasPrefix(part, "!")) {
			continue
		}
		line += part
		slashes := len(line) - len(strings.TrimRight(line, "\\"))
		continuing = slashes%2 == 1
		if continuing {
			line = line[:len(line)-1]
			continue
		}
		separator := len(line)
		for i := 0; i < len(line); i++ {
			if line[i] == '\\' {
				i++
				continue
			}
			if strings.ContainsRune("=: \t\f", rune(line[i])) {
				separator = i
				break
			}
		}
		key, err := unescapeProperty(line[:separator])
		if err != nil {
			return nil, err
		}
		value := strings.TrimLeft(line[separator:], " \t\f")
		if strings.HasPrefix(value, "=") || strings.HasPrefix(value, ":") {
			value = value[1:]
		}
		value, err = unescapeProperty(strings.TrimLeft(value, " \t\f"))
		if err != nil {
			return nil, err
		}
		properties[key] = value
		line = ""
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if continuing {
		return nil, fmt.Errorf("unfinished property continuation")
	}
	return properties, nil
}

func unescapeProperty(value string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			result.WriteByte(value[i])
			continue
		}
		i++
		if i >= len(value) {
			return "", fmt.Errorf("unfinished property escape")
		}
		switch value[i] {
		case 't':
			result.WriteByte('\t')
		case 'r':
			result.WriteByte('\r')
		case 'n':
			result.WriteByte('\n')
		case 'f':
			result.WriteByte('\f')
		case 'u':
			if i+4 >= len(value) {
				return "", fmt.Errorf("invalid Unicode property escape")
			}
			code, err := strconv.ParseUint(value[i+1:i+5], 16, 16)
			if err != nil {
				return "", fmt.Errorf("invalid Unicode property escape")
			}
			i += 4
			r := rune(code)
			if utf16.IsSurrogate(r) && i+6 < len(value) && value[i+1:i+3] == "\\u" {
				low, err := strconv.ParseUint(value[i+3:i+7], 16, 16)
				if err != nil {
					return "", fmt.Errorf("invalid Unicode property escape")
				}
				r = utf16.DecodeRune(r, rune(low))
				i += 6
			}
			result.WriteRune(r)
		default:
			result.WriteByte(value[i])
		}
	}
	return result.String(), nil
}
