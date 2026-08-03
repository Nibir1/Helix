// internal/ai/local_runtime.go
// Purpose: llama.cpp server lifecycle for the AI facade.
package ai

import (
	"context"

	"helix/internal/llamacpp"
)

var llamaServer *llamacpp.Server

// EnsureLlamaCppServer starts a llama.cpp server for the given model path.
func EnsureLlamaCppServer(ctx context.Context, modelPath string) error {
	binary, err := llamacpp.EnsureRuntime(ctx)
	if err != nil {
		return err
	}

	if llamaServer != nil {
		if llamaServer.ModelPath == modelPath && llamaServer.Running() {
			return nil
		}

		_ = llamaServer.Stop()
		llamaServer = nil
	}

	llamaServer = llamacpp.NewServer(binary, modelPath, 8081)

	if err := llamaServer.Start(ctx); err != nil {
		return err
	}

	UseModel("helix-local")
	return nil
}

// StopLocalRuntimes stops local provider processes.
func StopLocalRuntimes() {
	if llamaServer != nil {
		_ = llamaServer.Stop()
		llamaServer = nil
	}
}

// LlamaServerEndpoint returns the active llama.cpp endpoint.
func LlamaServerEndpoint() string {
	if llamaServer == nil {
		return ""
	}

	return llamaServer.Endpoint()
}
