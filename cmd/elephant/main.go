package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/ttab/clitools"
	"github.com/ttab/elephant-api/index"
	"github.com/ttab/elephant-api/repository"
	"github.com/ttab/elephant-cli/internal/apicli"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
)

const appName = "elephant-cli"

func main() {
	err := clitools.LoadEnv(appName)
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
			&cli.StringFlag{
				Name:      "out",
				Usage:     "File to write to, optional",
				TakesFile: true,
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
			&cli.StringFlag{
				Name:  "status",
				Usage: "Status to set on the copied document (e.g. usable)",
			},
			&cli.StringSliceFlag{
				Name:  "access",
				Usage: "ACL entry as 'perms:uri' (e.g. 'rw:core://unit/redaktionen'). Repeat to add more entries; replaces the default ACL when set.",
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
			&cli.StringFlag{
				Name:  "status",
				Usage: "Status to set on each copied document (e.g. usable)",
			},
			&cli.StringSliceFlag{
				Name:  "access",
				Usage: "ACL entry as 'perms:uri' (e.g. 'rw:core://unit/redaktionen'). Repeat to add more entries; replaces the default ACL when set.",
			},
		},
	}

	listDocumentsCommand := cli.Command{
		Name:   "docs-list",
		Action: listDocuments,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "doc-type",
				Usage:    "Type of documents to list",
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

	aclUpdateCommand := cli.Command{
		Name:   "acl-update",
		Action: aclUpdate,
		Usage:  "ensure ACL entries are set on a document or every document of a type",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "uuid",
				Usage: "UUID of the document to update",
			},
			&cli.StringFlag{
				Name:  "doc-type",
				Usage: "Type of documents to update in bulk",
			},
			&cli.StringSliceFlag{
				Name:  "access",
				Usage: "ACL entry as 'perms:uri' (e.g. 'rw:core://unit/redaktionen'). Repeat to add more entries.",
			},
		},
	}

	runListCommand := cli.Command{
		Name:      "run-list",
		Action:    runListAction,
		Usage:     "run an action on the documents listed in a CSV file",
		ArgsUsage: "CSV file",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "separator",
				Aliases: []string{"f"},
				Value:   ",",
			},
			&cli.StringFlag{
				Name:  "id-field",
				Value: "_id",
			},
			&cli.StringFlag{
				Name:  "selected-field",
				Usage: "The name of a boolean field that should be true to include the document for the action",
			},
			&cli.BoolFlag{
				Name:  "unpublish",
				Usage: "Unpublish the documents",
			},
		},
	}

	commands := []*cli.Command{
		&docVersionsCommand,
		&downloadDocCommand,
		&uploadDocCommand,
		&copyDocumentCommand,
		&copyDocumentsCommand,
		&listDocumentsCommand,
		&searchCommand,
		&setStatusCommand,
		&aclUpdateCommand,
		&runListCommand,
		clitools.ConfigureCliCommands(
			appName, clitools.DefaultApplicationID,
		),
	}

	commands = append(commands, apicli.AllServiceCommands()...)

	app := cli.Command{
		Name:     "elephant",
		Usage:    "The elephant cli tool",
		Commands: commands,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "env",
				Usage:   "Environment (local/stage/prod)",
				Sources: cli.EnvVars("ENV"),
				Value:   "stage",
			},
			&cli.StringSliceFlag{
				Name:    "scopes",
				Usage:   "OAuth2 scopes to request",
				Sources: cli.EnvVars("ELEPHANT_SCOPES"),
				Value:   apicli.DefaultScopes,
			},
			&cli.BoolFlag{
				Name:  "fmt-toon",
				Usage: "Use toon format for input/output instead of JSON",
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		println("error: ", err.Error())
		os.Exit(1)
	}
}

type elephantClients struct {
	Env       string
	Documents repository.Documents
	Search    index.SearchV1
}

func getClients(
	ctx context.Context,
	env string,
) (*elephantClients, error) {
	conf, err := clitools.NewConfigurationHandler(
		appName, clitools.DefaultApplicationID, env,
	)
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	token, err := conf.GetAccessToken(ctx, []string{
		"baboon", "baboon_admin", "doc_read", "media", "doc_write",
		"asset_upload", "eventlog_read", "search",
	})
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	err = conf.Save()
	if err != nil {
		slog.Warn("failed to save configuration", "err", err)
	}

	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: token.Token,
	}))

	repoEndpoint, ok := conf.GetEndpoint("repository")
	if !ok {
		return nil, errors.New("no endpoint for repository")
	}

	indexEndpoint, ok := conf.GetEndpoint("index")
	if !ok {
		return nil, errors.New("no endpoint for index")
	}

	documents := repository.NewDocumentsProtobufClient(repoEndpoint, client)
	search := index.NewSearchV1ProtobufClient(indexEndpoint, client)

	return &elephantClients{
		Env:       env,
		Documents: documents,
		Search:    search,
	}, nil
}
