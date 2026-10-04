package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/cedar-policy/cedar-go"
)

type policyVersion struct {
	Version     int    `json:"version"`
	CedarSource string `json:"cedarSource"`
	Active      bool   `json:"active"`
	CreatedAt   string `json:"createdAt"`
}

func runPolicy(args []string) {
	if len(args) < 2 {
		usage()
		os.Exit(2)
	}
	sub, file := args[0], args[1]

	switch sub {
	case "validate":
		policyValidate(file)
	case "diff":
		policyDiff(file)
	case "apply":
		activate := false
		for _, a := range args[2:] {
			if a == "--activate" {
				activate = true
			}
		}
		policyApply(file, activate)
	default:
		usage()
		os.Exit(2)
	}
}

func readPolicyFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
		os.Exit(1)
	}
	return string(b)
}

// policyValidate checks the file compiles, without touching control-api
// or persisting anything — a pure syntax check, the kind a pre-commit
// hook or CI step would run on every PR touching a .cedar file.
func policyValidate(path string) {
	source := readPolicyFile(path)
	if _, err := cedar.NewPolicySetFromBytes(path, []byte(source)); err != nil {
		fmt.Fprintf(os.Stderr, "INVALID: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK: %s is valid Cedar\n", path)
}

// policyDiff shows what would change if this file were applied, against
// the version control-api currently has marked active — shells out to
// the system `diff` rather than reimplementing a line-diff algorithm.
func policyDiff(path string) {
	active := fetchActivePolicy()

	oldFile, err := os.CreateTemp("", "wardenctl-active-*.cedar")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create temp file: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(oldFile.Name())
	oldFile.WriteString(active.CedarSource)
	oldFile.Close()

	fmt.Printf("--- active (v%d)\n+++ %s\n", active.Version, path)
	cmd := exec.Command("diff", "-u", oldFile.Name(), path)
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	// diff exits 1 when inputs differ — that's not a tool failure, only
	// exit codes >= 2 are.
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() > 1 {
		fmt.Fprintf(os.Stderr, "diff failed: %v\n", err)
		os.Exit(1)
	}
	body := out.String()
	// Drop diff's own file-header lines (already printed our own above,
	// with the version number diff can't know about).
	lines := strings.SplitN(body, "\n", 3)
	if len(lines) == 3 {
		fmt.Print(lines[2])
	} else if body == "" {
		fmt.Println("(no differences)")
	} else {
		fmt.Print(body)
	}
}

// policyApply creates a new version from the file (control-api validates
// it server-side too — this isn't trusting the client-side check alone)
// and, with --activate, activates it in the same run.
func policyApply(path string, activate bool) {
	source := readPolicyFile(path)

	reqBody, _ := json.Marshal(map[string]string{"cedarSource": source})
	resp := controlAPIRequest("POST", "/policies", reqBody)
	var created struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		fmt.Fprintf(os.Stderr, "decode response: %v\n", err)
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Printf("created version %d (inactive)\n", created.Version)

	if !activate {
		fmt.Printf("not activated — re-run with --activate, or activate it from the console\n")
		return
	}
	activateResp := controlAPIRequest("POST", fmt.Sprintf("/policies/%d/activate", created.Version), nil)
	activateResp.Body.Close()
	fmt.Printf("activated version %d\n", created.Version)
}

func fetchActivePolicy() policyVersion {
	resp := controlAPIRequest("GET", "/policies", nil)
	defer resp.Body.Close()
	var versions []policyVersion
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		fmt.Fprintf(os.Stderr, "decode response: %v\n", err)
		os.Exit(1)
	}
	for _, v := range versions {
		if v.Active {
			return v
		}
	}
	fmt.Fprintln(os.Stderr, "no active policy version found")
	os.Exit(1)
	return policyVersion{}
}

func controlAPIRequest(method, path string, body []byte) *http.Response {
	baseURL := os.Getenv("CONTROL_API_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8081"
	}
	adminToken := os.Getenv("ADMIN_TOKEN")
	if adminToken == "" {
		fmt.Fprintln(os.Stderr, "ADMIN_TOKEN is required for policy subcommands")
		os.Exit(2)
	}

	var bodyReader *bytes.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, baseURL+path, bodyReader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "control-api request failed: %v\n", err)
		os.Exit(1)
	}
	if resp.StatusCode >= 300 {
		var errBody bytes.Buffer
		errBody.ReadFrom(resp.Body)
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "control-api returned %d: %s\n", resp.StatusCode, errBody.String())
		os.Exit(1)
	}
	return resp
}
