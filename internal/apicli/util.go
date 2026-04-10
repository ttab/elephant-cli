package apicli

//go:generate go run github.com/ttab/elephant-cli/cmd/generate-cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	toon "github.com/toon-format/toon-go"
	"github.com/ttab/clitools"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const appName = "elephant-cli"

// DefaultScopes are the read-only scopes requested by default.
var DefaultScopes = []string{
	"doc_read", "eventlog_read", "metrics_read",
	"schema_read", "search", "user",
}

// getClientConfig returns an authenticated HTTP client and endpoint URL for the
// given endpoint name. It reads --env and --scopes from the CLI command.
func getClientConfig(
	ctx context.Context, cmd *cli.Command, endpointName string,
) (*http.Client, string, error) {
	env := cmd.String("env")
	scopes := cmd.StringSlice("scopes")

	conf, err := clitools.NewConfigurationHandler(
		appName, clitools.DefaultApplicationID, env,
	)
	if err != nil {
		return nil, "", fmt.Errorf("load configuration: %w", err)
	}

	token, err := conf.GetAccessToken(ctx, scopes)
	if err != nil {
		return nil, "", fmt.Errorf("get access token: %w", err)
	}

	if err := conf.Save(); err != nil {
		slog.Warn("save configuration", "err", err)
	}

	httpClient := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token.Token,
	}))

	endpoint, ok := conf.GetEndpoint(endpointName)
	if !ok {
		return nil, "", fmt.Errorf("no endpoint configured for %q", endpointName)
	}

	return httpClient, endpoint, nil
}

// readRequest reads a protobuf request message from the file specified by the
// --file flag. If --file is not set, it returns nil (the caller should populate
// the request from flags). Supports JSON and toon (when --fmt-toon is set).
func readRequest(cmd *cli.Command, msg proto.Message) error {
	file := cmd.String("file")
	if file == "" {
		return nil
	}

	var (
		r      io.Reader
		closer io.Closer
	)

	if file == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("open input file: %w", err)
		}

		r = f
		closer = f
	}

	if closer != nil {
		defer func() {
			if err := closer.Close(); err != nil {
				slog.Warn("close input file", "err", err)
			}
		}()
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read input: %w", err)
	}

	if cmd.Bool("fmt-toon") {
		decoded, err := toon.Decode(data)
		if err != nil {
			return fmt.Errorf("decode toon input: %w", err)
		}

		data, err = json.Marshal(decoded)
		if err != nil {
			return fmt.Errorf("convert toon to JSON: %w", err)
		}
	}

	if err := protojson.Unmarshal(data, msg); err != nil {
		return fmt.Errorf("unmarshal request: %w", err)
	}

	return nil
}

// writeResponse marshals a protobuf response message to stdout as JSON or toon.
func writeResponse(cmd *cli.Command, msg proto.Message) error {
	opts := protojson.MarshalOptions{
		Multiline: true,
		Indent:    "  ",
	}

	data, err := opts.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	if cmd.Bool("fmt-toon") {
		var m any

		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("parse JSON for toon conversion: %w", err)
		}

		data, err = toon.Marshal(m)
		if err != nil {
			return fmt.Errorf("marshal toon output: %w", err)
		}
	}

	_, err = os.Stdout.Write(data)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	fmt.Println()

	return nil
}

// methodFlags returns the common flags used by all generated method commands.
func methodFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "file",
			Aliases: []string{"f"},
			Usage:   "Read request from JSON/toon file (use '-' for stdin)",
		},
		&cli.BoolFlag{
			Name:  "req-schema",
			Usage: "Print JSON Schema for the request message and exit",
		},
		&cli.BoolFlag{
			Name:  "res-schema",
			Usage: "Print JSON Schema for the response message and exit",
		},
	}
}

// parseEnum converts a string enum value name to its int32 representation.
func parseEnum(value string, valueMap map[string]int32) (int32, error) {
	v, ok := valueMap[value]
	if !ok {
		return 0, fmt.Errorf("unknown enum value %q", value)
	}

	return v, nil
}
