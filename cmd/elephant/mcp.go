package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ttab/clitools"
	"github.com/ttab/elephant-cli/internal/apicli"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
)

func mcpCommand() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "Run an MCP server exposing elephant-api services as tools",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "http",
				Usage: "Listen address for HTTP transport (e.g. :8080). Uses stdio if not set.",
			},
		},
		Action: runMCPServer,
	}
}

type mcpEnv struct {
	httpClient *http.Client
	conf       *clitools.ConfigurationHandler
}

func runMCPServer(ctx context.Context, cmd *cli.Command) error {
	env := cmd.String("env")
	scopes := cmd.StringSlice("scopes")
	httpAddr := cmd.String("http")

	conf, err := clitools.NewConfigurationHandler(
		appName, clitools.DefaultApplicationID, env,
	)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	token, err := conf.GetAccessToken(ctx, scopes)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	if err := conf.Save(); err != nil {
		slog.Warn("save configuration", "err", err)
	}

	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token.Token,
	}))

	e := &mcpEnv{
		httpClient: httpClient,
		conf:       conf,
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "elephant",
		Version: "1.0.0",
	}, nil)

	registerMCPTools(server, e)

	if httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(
			func(_ *http.Request) *mcp.Server {
				return server
			}, nil)

		log.Printf("elephant MCP server listening on %s", httpAddr)

		return http.ListenAndServe(httpAddr, handler)
	}

	return server.Run(ctx, &mcp.StdioTransport{})
}

// --- MCP tool input types ---

type listServicesInput struct{}

type searchMethodsInput struct {
	Query string `json:"query" jsonschema:"Search query to match against service names, method names, and descriptions"`
}

type getSchemaInput struct {
	Service string `json:"service" jsonschema:"Service name (e.g. Documents)"`
	Method  string `json:"method" jsonschema:"Method name (e.g. Get)"`
	Type    string `json:"type" jsonschema:"Schema type: 'request' or 'response'"`
}

type callMethodInput struct {
	Service string         `json:"service" jsonschema:"Service name (e.g. Documents)"`
	Method  string         `json:"method" jsonschema:"Method name (e.g. Get)"`
	Input   map[string]any `json:"input,omitempty" jsonschema:"Request payload as JSON object (omit for methods with no required fields)"`
}

// --- MCP tool registration ---

func registerMCPTools(server *mcp.Server, e *mcpEnv) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_list_services",
		Description: "List all available elephant-api services and their methods. " +
			"Use this to discover what API operations are available.",
	}, func(
		_ context.Context, _ *mcp.CallToolRequest, _ listServicesInput,
	) (*mcp.CallToolResult, any, error) {
		return handleListServices()
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_search_methods",
		Description: "Search elephant-api methods by keyword. " +
			"Matches against service names, method names, and descriptions.",
	}, func(
		_ context.Context, _ *mcp.CallToolRequest, input searchMethodsInput,
	) (*mcp.CallToolResult, any, error) {
		return handleSearchMethods(input)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_get_schema",
		Description: "Get the JSON Schema for a method's request or response message. " +
			"Use this to understand the structure of API payloads before calling a method.",
	}, func(
		_ context.Context, _ *mcp.CallToolRequest, input getSchemaInput,
	) (*mcp.CallToolResult, any, error) {
		return handleGetSchema(input)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_call",
		Description: "Call an elephant-api method. " +
			"The input should be a JSON object matching the method's request schema. " +
			"Use elephant_get_schema first to understand the expected payload.",
	}, func(
		ctx context.Context, _ *mcp.CallToolRequest, input callMethodInput,
	) (*mcp.CallToolResult, any, error) {
		return handleCallMethod(ctx, e, input)
	})
}

// --- MCP tool handlers ---

func handleListServices() (*mcp.CallToolResult, any, error) {
	var sb strings.Builder

	for _, svc := range apicli.Registry {
		fmt.Fprintf(&sb, "## %s\n", svc.Name)

		if svc.Description != "" {
			fmt.Fprintf(&sb, "%s\n", svc.Description)
		}

		fmt.Fprintf(&sb, "Endpoint: %s\n\n", svc.Endpoint)

		for _, m := range svc.Methods {
			fmt.Fprintf(&sb, "- **%s.%s**", m.Service, m.Method)

			if m.Description != "" {
				fmt.Fprintf(&sb, " — %s", m.Description)
			}

			sb.WriteByte('\n')
		}

		sb.WriteByte('\n')
	}

	return mcpText(sb.String()), nil, nil
}

func handleSearchMethods(input searchMethodsInput) (*mcp.CallToolResult, any, error) {
	if input.Query == "" {
		return mcpError("query is required"), nil, nil
	}

	results := apicli.SearchMethods(input.Query)

	if len(results) == 0 {
		return mcpText(fmt.Sprintf("No methods found matching %q.", input.Query)), nil, nil
	}

	var sb strings.Builder

	fmt.Fprintf(&sb, "Found %d methods matching %q:\n\n", len(results), input.Query)

	for _, m := range results {
		fmt.Fprintf(&sb, "- **%s.%s**", m.Service, m.Method)

		if m.Description != "" {
			fmt.Fprintf(&sb, " — %s", m.Description)
		}

		sb.WriteByte('\n')
	}

	return mcpText(sb.String()), nil, nil
}

func handleGetSchema(input getSchemaInput) (*mcp.CallToolResult, any, error) {
	m := apicli.FindMethod(input.Service, input.Method)
	if m == nil {
		return mcpError(fmt.Sprintf(
			"Method %s.%s not found. Use elephant_list_services to see available methods.",
			input.Service, input.Method,
		)), nil, nil
	}

	var schema string

	switch strings.ToLower(input.Type) {
	case "request", "req":
		schema = m.ReqSchema
	case "response", "res":
		schema = m.ResSchema
	default:
		return mcpError("type must be 'request' or 'response'"), nil, nil
	}

	return mcpText(schema), nil, nil
}

func handleCallMethod(ctx context.Context, e *mcpEnv, input callMethodInput) (*mcp.CallToolResult, any, error) {
	m := apicli.FindMethod(input.Service, input.Method)
	if m == nil {
		return mcpError(fmt.Sprintf(
			"Method %s.%s not found. Use elephant_list_services to see available methods.",
			input.Service, input.Method,
		)), nil, nil
	}

	endpoint, ok := e.conf.GetEndpoint(m.Endpoint)
	if !ok {
		return mcpError(fmt.Sprintf(
			"No endpoint configured for %q. Run: elephant configure --endpoint %s=<url>",
			m.Endpoint, m.Endpoint,
		)), nil, nil
	}

	var inputJSON string

	if input.Input != nil {
		data, err := json.Marshal(input.Input)
		if err != nil {
			return mcpError(fmt.Sprintf("marshal input: %v", err)), nil, nil
		}

		inputJSON = string(data)
	}

	result, err := m.Call(ctx, e.httpClient, endpoint, inputJSON)
	if err != nil {
		return mcpError(fmt.Sprintf("API error: %v", err)), nil, nil
	}

	return mcpText(result), nil, nil
}

func mcpText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}

func mcpError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
		IsError: true,
	}
}
