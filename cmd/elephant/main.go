package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/ttab/clitools"
	"github.com/ttab/elephant-api/index"
	rpc_newsdoc "github.com/ttab/elephant-api/newsdoc"
	"github.com/ttab/elephant-api/repository"
	"github.com/urfave/cli/v2"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/encoding/protojson"
)

type envEndpoints struct {
	Repository string
	Index      string
}

var endpoints = map[string]envEndpoints{
	"local": {
		Repository: "https://repository.stage.tt.se",
		Index:      "https://elephant-index.stage.tt.se",
	},
	"stage": {
		Repository: "https://repository.stage.tt.se",
		Index:      "https://elephant-index.stage.tt.se",
	},
	"prod": {
		Repository: "https://repository.api.tt.ecms.se",
		Index:      "https://index.api.tt.ecms.se",
	},
}

func main() {
	err := clitools.LoadEnv("elephant")
	if err != nil {
		slog.Error("load environment config",
			"err", err)
		os.Exit(1)
	}

	docVersionsCommand := cli.Command{
		Name:   "doc-versions",
		Action: listDocVersions,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "uuid",
				Usage: "UUID of document to download",
			},
			&cli.StringFlag{
				Name:  "uri",
				Usage: "URI of document to download",
			},
		},
	}

	downloadDocCommand := cli.Command{
		Name:   "doc-download",
		Action: downloadDoc,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "uuid",
				Usage: "UUID of document to download",
			},
			&cli.StringFlag{
				Name:  "uri",
				Usage: "URI of document to download",
			},
			&cli.PathFlag{
				Name:  "out",
				Usage: "File to write to, optional",
			},
			&cli.Int64Flag{
				Name:  "version",
				Usage: "Document version",
			},
		},
	}

	uploadDocCommand := cli.Command{
		Name:   "doc-upload",
		Action: uploadDoc,
		Flags: []cli.Flag{
			&cli.Int64Flag{
				Name:  "if-match",
				Usage: "If the document matches this version",
			},
		},
	}

	copyDocumentCommand := cli.Command{
		Name:   "doc-copy",
		Action: copyDocument,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "uuid",
				Usage:    "UUID of document to copy",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "attachment",
				Usage:    "Attachment to copy",
				Required: false,
			},
			&cli.StringFlag{
				Name:     "to-env",
				Usage:    "Environment to copy to",
				Required: true,
			},
		},
	}

	copyDocumentsCommand := cli.Command{
		Name:   "docs-copy",
		Action: copyDocuments,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "doc-type",
				Usage:    "Type of documents to copy",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "attachment",
				Usage:    "Attachment to copy",
				Required: false,
			},
			&cli.StringFlag{
				Name:     "to-env",
				Usage:    "Environment to copy to",
				Required: true,
			},
		},
	}

	listDocumentsCommand := cli.Command{
		Name:   "docs-list",
		Action: listDocuments,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "doc-type",
				Usage:    "Type of documents to copy",
				Required: true,
			},
			&cli.IntFlag{
				Name:  "max",
				Usage: "Max number of documents to list",
				Value: 500,
			},
		},
	}

	searchCommand := cli.Command{
		Name:   "search",
		Action: queryIndex,
	}

	loginCommand := cli.Command{
		Name:   "login",
		Action: login,
	}

	setStatusCommand := cli.Command{
		Name:   "set-status",
		Action: setStatus,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "uuid",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "status",
				Required: true,
			},
			&cli.Int64Flag{
				Name: "version",
			},
			&cli.StringSliceFlag{
				Name:  "meta",
				Usage: "Meta values in the format 'key=value'",
			},
		},
	}

	app := cli.App{
		Name:  "elephant",
		Usage: "The elephant cli tool",
		Commands: []*cli.Command{
			&docVersionsCommand,
			&downloadDocCommand,
			&uploadDocCommand,
			&copyDocumentCommand,
			&copyDocumentsCommand,
			&listDocumentsCommand,
			&searchCommand,
			&setStatusCommand,
			&loginCommand,
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "env",
				Usage:   "Environment (local/stage/prod)",
				EnvVars: []string{"ENV"},
				Value:   "stage",
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		println("error: ", err.Error())
		os.Exit(1)
	}
}

func readNewsdoc(fileName string) (*rpc_newsdoc.Document, error) {
	var doc rpc_newsdoc.Document

	data, err := os.ReadFile(fileName)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	err = protojson.Unmarshal(data, &doc)
	if err != nil {
		return nil, fmt.Errorf("parse file: %w", err)
	}

	return &doc, nil
}

func login(c *cli.Context) error {
	env := c.String("env")

	_, err := getClients(c.Context, env, true)
	if err != nil {
		return err
	}

	return nil
}

type elephantClients struct {
	Env       string
	Documents repository.Documents
	Search    index.SearchV1
}

func getClients(
	ctx context.Context,
	env string,
	save bool,
) (*elephantClients, error) {
	var oidcURL string

	switch env {
	case "local", "stage":
		oidcURL = clitools.StageOIDCServer
	case "prod":
		oidcURL = clitools.ProdOIDCServer
	default:
		return nil, fmt.Errorf("unknown environment %q", env)
	}

	oidcConfig, err := clitools.OIDCConfigURL(oidcURL, "elephant")
	if err != nil {
		return nil, fmt.Errorf("get OIDC config URL: %w", err)
	}

	conf, err := clitools.NewConfigurationHandler[struct{}](
		"baboon-client", clitools.DefaultApplicationID, env,
		oidcConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	token, err := conf.GetAccessToken(ctx, env, true, []string{
		"baboon", "baboon_admin", "doc_read", "media", "doc_write",
		"asset_upload", "eventlog_read", "search",
	})
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	if save {
		err = conf.Save()
		if err != nil {
			slog.Warn("failed to save configuration", "err", err)
		}
	}

	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token.Token,
	}))

	endpoint, ok := endpoints[env]
	if !ok {
		return nil, fmt.Errorf("no baboon endpoint for %q", env)
	}

	documents := repository.NewDocumentsProtobufClient(endpoint.Repository, client)
	search := index.NewSearchV1ProtobufClient(endpoint.Index, client)

	return &elephantClients{
		Env:       env,
		Documents: documents,
		Search:    search,
	}, nil
}
