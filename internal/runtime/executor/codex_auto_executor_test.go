package executor

import (
	"context"
	"errors"
	"net/http"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type stubCodexHTTPExecutor struct {
	executeCalls       int
	executeStreamCalls int
	response           cliproxyexecutor.Response
	err                error
	streamResult       *cliproxyexecutor.StreamResult
	streamErr          error
}

func (s *stubCodexHTTPExecutor) PrepareRequest(_ *http.Request, _ *cliproxyauth.Auth) error {
	return nil
}

func (s *stubCodexHTTPExecutor) HttpRequest(_ context.Context, _ *cliproxyauth.Auth, _ *http.Request) (*http.Response, error) {
	return nil, nil
}

func (s *stubCodexHTTPExecutor) Execute(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	s.executeCalls++
	return s.response, s.err
}

func (s *stubCodexHTTPExecutor) ExecuteStream(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	s.executeStreamCalls++
	return s.streamResult, s.streamErr
}

func (s *stubCodexHTTPExecutor) Refresh(_ context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	return auth, nil
}

func (s *stubCodexHTTPExecutor) CountTokens(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

type stubCodexWebsocketExecutor struct {
	executeCalls       int
	executeStreamCalls int
	response           cliproxyexecutor.Response
	err                error
	streamResult       *cliproxyexecutor.StreamResult
	streamErr          error
}

func (s *stubCodexWebsocketExecutor) Execute(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	s.executeCalls++
	return s.response, s.err
}

func (s *stubCodexWebsocketExecutor) ExecuteStream(_ context.Context, _ *cliproxyauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	s.executeStreamCalls++
	return s.streamResult, s.streamErr
}

func (s *stubCodexWebsocketExecutor) CloseExecutionSession(string) {}

func TestCodexAutoExecutor_ExecuteSelectsExpectedTransport(t *testing.T) {
	downstreamWS := cliproxyexecutor.WithDownstreamWebsocket(context.Background())
	tests := []struct {
		name          string
		ctx           context.Context
		auth          *cliproxyauth.Auth
		wsErr         error
		wantWSCalls   int
		wantHTTPCalls int
		wantPayload   string
	}{
		{
			name: "direct websocket uses websocket executor",
			ctx:  downstreamWS,
			auth: &cliproxyauth.Auth{
				Provider:   "codex",
				Attributes: map[string]string{"websockets": "true"},
			},
			wantWSCalls:   1,
			wantHTTPCalls: 0,
			wantPayload:   "ws",
		},
		{
			name: "deno websocket uses websocket executor",
			ctx:  downstreamWS,
			auth: &cliproxyauth.Auth{
				Provider:   "codex",
				Attributes: map[string]string{"websockets": "true", "deno_proxy_host": "https://relay.example.com"},
			},
			wantWSCalls:   1,
			wantHTTPCalls: 0,
			wantPayload:   "ws",
		},
		{
			name: "deno websocket disabled uses http executor",
			ctx:  downstreamWS,
			auth: &cliproxyauth.Auth{
				Provider:   "codex",
				Attributes: map[string]string{"websockets": "false", "deno_proxy_host": "https://relay.example.com"},
			},
			wantWSCalls:   0,
			wantHTTPCalls: 1,
			wantPayload:   "http",
		},
		{
			name: "deno websocket fallback uses http executor",
			ctx:  downstreamWS,
			auth: &cliproxyauth.Auth{
				Provider:   "codex",
				Attributes: map[string]string{"websockets": "true", "deno_proxy_host": "https://relay.example.com"},
			},
			wsErr:         codexDenoWebsocketFallbackError{cause: errors.New("handshake failed")},
			wantWSCalls:   1,
			wantHTTPCalls: 1,
			wantPayload:   "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpExec := &stubCodexHTTPExecutor{
				response: cliproxyexecutor.Response{Payload: []byte("http")},
			}
			wsExec := &stubCodexWebsocketExecutor{
				response: cliproxyexecutor.Response{Payload: []byte("ws")},
				err:      tt.wsErr,
			}
			exec := &CodexAutoExecutor{
				httpExec: httpExec,
				wsExec:   wsExec,
			}

			resp, err := exec.Execute(tt.ctx, tt.auth, cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if wsExec.executeCalls != tt.wantWSCalls {
				t.Fatalf("websocket execute calls = %d, want %d", wsExec.executeCalls, tt.wantWSCalls)
			}
			if httpExec.executeCalls != tt.wantHTTPCalls {
				t.Fatalf("http execute calls = %d, want %d", httpExec.executeCalls, tt.wantHTTPCalls)
			}
			if got := string(resp.Payload); got != tt.wantPayload {
				t.Fatalf("response payload = %q, want %q", got, tt.wantPayload)
			}
		})
	}
}

func TestCodexAutoExecutor_ExecuteStreamFallsBackFromDenoWebsocketRelay(t *testing.T) {
	httpStream := &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Test": []string{"http"}}}
	exec := &CodexAutoExecutor{
		httpExec: &stubCodexHTTPExecutor{streamResult: httpStream},
		wsExec: &stubCodexWebsocketExecutor{
			streamErr: codexDenoWebsocketFallbackError{cause: errors.New("relay unavailable")},
		},
	}
	auth := &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"websockets": "true", "deno_proxy_host": "https://relay.example.com"},
	}

	result, err := exec.ExecuteStream(
		cliproxyexecutor.WithDownstreamWebsocket(context.Background()),
		auth,
		cliproxyexecutor.Request{},
		cliproxyexecutor.Options{},
	)
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	if result != httpStream {
		t.Fatal("expected ExecuteStream() to return the HTTP fallback result")
	}
}
