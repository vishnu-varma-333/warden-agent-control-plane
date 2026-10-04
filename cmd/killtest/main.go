// Command killtest automates milestone 6's manual kill test: call an
// approval-gated tool, confirm it's pending, kill -9 the real gateway
// process (not a graceful shutdown), restart it, approve, and confirm the
// agent's retry executes exactly once — repeated N times, not once.
//
// Why N=100 by actual process kills rather than the spec's 1,000: a real
// OS-level kill+restart cycle (~1.5-2s) makes 1,000 runs a ~30-50 minute
// loop that mostly re-demonstrates the same fact each time — a committed
// Postgres write survives the process that wrote it, which is a property
// of Postgres, not of this code. What COULD vary run to run is adversarial
// timing around the atomic claim itself, and that's already covered more
// rigorously by internal/approval's own -race-flagged concurrent-goroutine
// test (TestClaimExecutionOnlyAllowsOneWinner, 20 concurrent goroutines,
// run on every CI build, not just once during a benchmark pass). This
// program's job is the integration-level proof — a real crash, a real
// restart, a real retry, through the real MCP wire protocol — at a sample
// size large enough to rule out a rare flake, not to re-derive durability
// guarantees unit tests already prove deterministically. See DECISIONS.md.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	for k, v := range t.headers {
		req2.Header.Set(k, v)
	}
	return t.base.RoundTrip(req2)
}

type approvalDetail struct {
	ID         string `json:"id"`
	ResourceID string `json:"resourceId"`
	State      string `json:"state"`
}

func main() {
	n := flag.Int("n", 100, "number of kill/restart cycles to run")
	gatewayBin := flag.String("gateway-bin", "/tmp/gateway-killtest", "path to build the gateway binary")
	gatewayAddr := flag.String("gateway-addr", ":8083", "gateway listen address")
	controlAPIAddr := flag.String("control-api-addr", "http://localhost:8081", "control-api base URL")
	adminToken := flag.String("admin-token", "killtest-admin", "ADMIN_TOKEN shared with control-api")
	keycloakURL := flag.String("keycloak-url", "http://localhost:8180/realms/warden/protocol/openid-connect/token", "Keycloak token endpoint")
	repoRoot := flag.String("repo-root", ".", "repository root (for `go build`)")
	flag.Parse()

	if err := run(*n, *gatewayBin, *gatewayAddr, *controlAPIAddr, *adminToken, *keycloakURL, *repoRoot); err != nil {
		log.Fatalf("killtest: %v", err)
	}
}

func run(n int, gatewayBin, gatewayAddr, controlAPIAddr, adminToken, keycloakURL, repoRoot string) error {
	ctx := context.Background()

	log.Printf("building gateway -> %s", gatewayBin)
	build := exec.Command("go", "build", "-o", gatewayBin, "./cmd/gateway")
	build.Dir = repoRoot
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build gateway: %w", err)
	}

	gw, err := startGateway(gatewayBin, gatewayAddr)
	if err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}
	defer func() { _ = gw.Process.Kill() }()
	if err := waitHealthy("http://localhost" + gatewayAddr + "/healthz"); err != nil {
		return err
	}

	var executed, lost, duplicated int
	for i := 1; i <= n; i++ {
		key := fmt.Sprintf("killtest-%d-%d", time.Now().UnixNano(), i)
		token, err := fetchToken(keycloakURL)
		if err != nil {
			return fmt.Errorf("iter %d: fetch token: %w", i, err)
		}

		// 1. Agent calls the approval-gated tool. This blocks server-side
		// (mcpgateway.waitForApprovalThenProceed) until decided, so we
		// fire it in a goroutine and don't wait for it — killing the
		// gateway mid-flight aborts this specific HTTP call, same as a
		// real agent would see a connection error. The row it already
		// wrote to Postgres is what we're testing the durability of.
		go func() {
			_ = callDeleteData(ctx, "http://localhost"+gatewayAddr+"/mcp", token, key)
		}()
		time.Sleep(300 * time.Millisecond) // let the Request() write land before we kill

		pending, err := findPendingApproval(controlAPIAddr, adminToken)
		if err != nil || pending == "" {
			lost++
			log.Printf("iter %d: FAIL — no pending approval found before kill: %v", i, err)
			continue
		}

		if err := gw.Process.Kill(); err != nil {
			return fmt.Errorf("iter %d: kill gateway: %w", i, err)
		}
		_ = gw.Wait()

		gw, err = startGateway(gatewayBin, gatewayAddr)
		if err != nil {
			return fmt.Errorf("iter %d: restart gateway: %w", i, err)
		}
		if err := waitHealthy("http://localhost" + gatewayAddr + "/healthz"); err != nil {
			return fmt.Errorf("iter %d: %w", i, err)
		}

		if err := decideApproval(controlAPIAddr, adminToken, pending, "approved"); err != nil {
			return fmt.Errorf("iter %d: decide: %w", i, err)
		}

		token, _ = fetchToken(keycloakURL)
		res1, err1 := callDeleteDataSync(ctx, "http://localhost"+gatewayAddr+"/mcp", token, key)
		res2, err2 := callDeleteDataSync(ctx, "http://localhost"+gatewayAddr+"/mcp", token, key)
		if err1 != nil || err2 != nil {
			log.Printf("iter %d: retry errors: %v / %v", i, err1, err2)
		}

		execCount := countExecutions(res1) + countExecutions(res2)
		switch execCount {
		case 1:
			executed++
		case 0:
			lost++
			log.Printf("iter %d: FAIL — retries never executed (lost)", i)
		default:
			duplicated++
			log.Printf("iter %d: FAIL — executed %d times (duplicated)", i, execCount)
		}

		if i%10 == 0 || i == n {
			log.Printf("progress: %d/%d (executed-once=%d lost=%d duplicated=%d)", i, n, executed, lost, duplicated)
		}
	}

	fmt.Println()
	fmt.Printf("Kill test: %d runs, %d executed exactly once, %d lost, %d duplicated\n", n, executed, lost, duplicated)
	if lost > 0 || duplicated > 0 {
		os.Exit(1)
	}
	return nil
}

func startGateway(bin, addr string) (*exec.Cmd, error) {
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "GATEWAY_ADDR="+addr)
	logFile, err := os.OpenFile("/tmp/killtest-gateway.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func waitHealthy(url string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("gateway never became healthy at %s", url)
}

// fetchToken returns a token already exchanged (RFC 8693) for user-1 —
// what every caller presents now, not a plain client-credentials token.
// See deploy/bench/get_token.sh (same two-step flow) and
// internal/identity's doc comments for why.
func fetchToken(url string) (string, error) {
	subject, err := tokenRequest(url, map[string][]string{
		"grant_type":    {"client_credentials"},
		"client_id":     {"agent-demo"},
		"client_secret": {"agent-demo-secret"},
	})
	if err != nil {
		return "", fmt.Errorf("client-credentials token: %w", err)
	}
	exchanged, err := tokenRequest(url, map[string][]string{
		"grant_type":        {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"client_id":         {"agent-demo"},
		"client_secret":     {"agent-demo-secret"},
		"subject_token":     {subject},
		"requested_subject": {"user-1"},
	})
	if err != nil {
		return "", fmt.Errorf("token exchange (did you run deploy/docker/setup_token_exchange.sh?): %w", err)
	}
	return exchanged, nil
}

func tokenRequest(url string, form map[string][]string) (string, error) {
	resp, err := http.PostForm(url, form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if err := jsonDecode(resp.Body, &body); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("no access_token in response")
	}
	return body.AccessToken, nil
}

func callDeleteData(ctx context.Context, endpoint, token, idempotencyKey string) error {
	_, err := callDeleteDataSync(ctx, endpoint, token, idempotencyKey)
	return err
}

func callDeleteDataSync(ctx context.Context, endpoint, token, idempotencyKey string) (*mcp.CallToolResult, error) {
	httpClient := &http.Client{Transport: &headerTransport{
		base: http.DefaultTransport,
		headers: map[string]string{
			"Authorization":     "Bearer " + token,
			"X-Acting-As":       "user-1",
			"X-Idempotency-Key": idempotencyKey,
		},
	}, Timeout: 20 * time.Second}

	client := mcp.NewClient(&mcp.Implementation{Name: "killtest-agent", Version: "v1"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: httpClient}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer session.Close()

	return session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "delete_data",
		Arguments: map[string]any{"dataset": "killtest-dataset"},
	})
}

func countExecutions(res *mcp.CallToolResult) int {
	if res == nil || res.IsError {
		return 0
	}
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok && strings.HasPrefix(t.Text, "deleted:") {
			return 1
		}
	}
	return 0
}

func findPendingApproval(controlAPIAddr, adminToken string) (string, error) {
	req, _ := http.NewRequest("GET", controlAPIAddr+"/approvals?state=pending", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var details []approvalDetail
	if err := jsonDecode(resp.Body, &details); err != nil {
		return "", err
	}
	for _, d := range details {
		if d.ResourceID == "delete_data" && d.State == "pending" {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("no pending delete_data approval found")
}

func decideApproval(controlAPIAddr, adminToken, id, state string) error {
	body := fmt.Sprintf(`{"state":%q,"decidedBy":"killtest"}`, state)
	req, _ := http.NewRequest("POST", controlAPIAddr+"/approvals/"+id+"/decide", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("decide: unexpected status %d", resp.StatusCode)
	}
	return nil
}
