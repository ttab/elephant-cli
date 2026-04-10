package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ttab/elephant-cli/internal/apicli"
	"github.com/ttab/revisor"
)

// --- Schema cache ---

type schemaCache struct {
	mu      sync.Mutex
	schemas []parsedSchema
	loaded  bool
}

type parsedSchema struct {
	Name    string
	Version string
	Spec    revisor.ConstraintSet
}

func (sc *schemaCache) get(ctx context.Context, e *mcpEnv) ([]parsedSchema, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.loaded {
		return sc.schemas, nil
	}

	schemas, err := fetchSchemas(ctx, e)
	if err != nil {
		return nil, err
	}

	sc.schemas = schemas
	sc.loaded = true

	return schemas, nil
}

type apiSchemaResponse struct {
	Schemas []apiSchemaEntry `json:"schemas"`
}

type apiSchemaEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Spec    string `json:"spec"`
}

func fetchSchemas(ctx context.Context, e *mcpEnv) ([]parsedSchema, error) {
	m := apicli.FindMethod("repository.Schemas", "GetAllActive")
	if m == nil {
		return nil, fmt.Errorf("method Schemas.GetAllActive not found in registry")
	}

	endpoint, ok := e.conf.GetEndpoint(m.Endpoint)
	if !ok {
		return nil, fmt.Errorf(
			"no endpoint configured for %q", m.Endpoint)
	}

	result, err := m.Call(ctx, e.httpClient, endpoint, "")
	if err != nil {
		return nil, fmt.Errorf("call Schemas.GetAllActive: %w", err)
	}

	var resp apiSchemaResponse

	err = json.Unmarshal([]byte(result), &resp)
	if err != nil {
		return nil, fmt.Errorf("unmarshal schema response: %w", err)
	}

	schemas := make([]parsedSchema, 0, len(resp.Schemas))

	for _, entry := range resp.Schemas {
		var spec revisor.ConstraintSet

		err := json.Unmarshal([]byte(entry.Spec), &spec)
		if err != nil {
			return nil, fmt.Errorf("parse spec for %q: %w",
				entry.Name, err)
		}

		schemas = append(schemas, parsedSchema{
			Name:    entry.Name,
			Version: entry.Version,
			Spec:    spec,
		})
	}

	return schemas, nil
}

// --- Input types ---

type schemaOverviewInput struct{}

type typeConstraintsInput struct {
	DocumentType string `json:"document_type" jsonschema:"Document type to look up (e.g. 'core/article', 'tt/wire')"`
}

type resolveRefInput struct {
	Ref  string `json:"ref" jsonschema:"Block ref URI to resolve (e.g. 'core://newsvalue', 'core://teaser')"`
	Kind string `json:"kind,omitempty" jsonschema:"Block kind: 'link', 'meta', or 'content'. If omitted, searches all kinds."`
}

type enumValuesInput struct {
	Ref string `json:"ref" jsonschema:"Enum ref URI to look up (e.g. 'core://place-types')"`
}

type indexMappingsInput struct {
	DocumentType string `json:"document_type" jsonschema:"Document type to get mappings for (e.g. 'core/article', 'tt/wire')"`
	Filter       string `json:"filter,omitempty" jsonschema:"Filter field names containing this substring (case-insensitive). Omit to list all fields."`
}

// --- Tool registration ---

func registerSchemaTools(server *mcp.Server, e *mcpEnv) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_schema_overview",
		Description: "List all active validation schemas with the document types " +
			"each declares or extends, and counts of reusable block definitions.",
	}, func(
		ctx context.Context, _ *mcp.CallToolRequest, _ schemaOverviewInput,
	) (*mcp.CallToolResult, any, error) {
		return handleSchemaOverview(ctx, e)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_type_constraints",
		Description: "Get the combined constraints for a document type. " +
			"Merges all declares and match entries across active schemas " +
			"to show allowed meta, content, and link blocks with their attributes.",
	}, func(
		ctx context.Context, _ *mcp.CallToolRequest, input typeConstraintsInput,
	) (*mcp.CallToolResult, any, error) {
		return handleTypeConstraints(ctx, e, input)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_resolve_ref",
		Description: "Resolve a reusable block ref URI (e.g. 'core://newsvalue') " +
			"to its full block definition including declares signature, " +
			"attributes, data constraints, and nested blocks.",
	}, func(
		ctx context.Context, _ *mcp.CallToolRequest, input resolveRefInput,
	) (*mcp.CallToolResult, any, error) {
		return handleResolveRef(ctx, e, input)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_enum_values",
		Description: "Get the allowed values for an enum ref URI. " +
			"Merges the base declaration with any extensions from other schemas.",
	}, func(
		ctx context.Context, _ *mcp.CallToolRequest, input enumValuesInput,
	) (*mcp.CallToolResult, any, error) {
		return handleEnumValues(ctx, e, input)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "elephant_index_mappings",
		Description: "Get searchable index field mappings for a document type. " +
			"Shows field names, types, and sub-fields available for search queries. " +
			"Use the filter parameter to find specific fields by name.",
	}, func(
		ctx context.Context, _ *mcp.CallToolRequest, input indexMappingsInput,
	) (*mcp.CallToolResult, any, error) {
		return handleIndexMappings(ctx, e, input)
	})
}

// --- Handlers ---

func handleSchemaOverview(
	ctx context.Context, e *mcpEnv,
) (*mcp.CallToolResult, any, error) {
	schemas, err := e.schemas.get(ctx, e)
	if err != nil {
		return mcpError(fmt.Sprintf("load schemas: %v", err)), nil, nil
	}

	var sb strings.Builder

	for _, s := range schemas {
		fmt.Fprintf(&sb, "## %s (%s)\n", s.Name, s.Version)

		var declares, extends []string

		for _, doc := range s.Spec.Documents {
			if doc.Declares != "" {
				declares = append(declares, doc.Declares)
			} else if mt := extractMatchType(doc); mt != "" {
				extends = append(extends, mt)
			}
		}

		if len(declares) > 0 {
			fmt.Fprintf(&sb, "Declares: %s\n",
				strings.Join(declares, ", "))
		}

		if len(extends) > 0 {
			fmt.Fprintf(&sb, "Extends: %s\n",
				strings.Join(extends, ", "))
		}

		var defs []string

		if n := len(s.Spec.Links); n > 0 {
			defs = append(defs, fmt.Sprintf("%d links", n))
		}

		if n := len(s.Spec.Meta); n > 0 {
			defs = append(defs, fmt.Sprintf("%d meta", n))
		}

		if n := len(s.Spec.Content); n > 0 {
			defs = append(defs, fmt.Sprintf("%d content", n))
		}

		if n := len(s.Spec.Enums); n > 0 {
			defs = append(defs, fmt.Sprintf("%d enums", n))
		}

		if len(defs) > 0 {
			fmt.Fprintf(&sb, "Reusable definitions: %s\n",
				strings.Join(defs, ", "))
		}

		sb.WriteByte('\n')
	}

	return mcpText(sb.String()), nil, nil
}

func handleTypeConstraints(
	ctx context.Context, e *mcpEnv, input typeConstraintsInput,
) (*mcp.CallToolResult, any, error) {
	if input.DocumentType == "" {
		return mcpError("document_type is required"), nil, nil
	}

	schemas, err := e.schemas.get(ctx, e)
	if err != nil {
		return mcpError(fmt.Sprintf("load schemas: %v", err)), nil, nil
	}

	var sb strings.Builder

	found := false

	for _, s := range schemas {
		for _, doc := range s.Spec.Documents {
			if !matchesDocType(doc, input.DocumentType) {
				continue
			}

			found = true

			rel := "declares"
			if doc.Declares == "" {
				rel = "extends"
			}

			fmt.Fprintf(&sb, "## %s (%s, %s %s)\n\n",
				doc.Name, s.Name, rel, input.DocumentType)

			if doc.Description != "" {
				fmt.Fprintf(&sb, "%s\n\n", doc.Description)
			}

			writeBlockList(&sb, "Links", doc.Links)
			writeBlockList(&sb, "Meta", doc.Meta)
			writeBlockList(&sb, "Content", doc.Content)

			if len(doc.Attributes.Keys) > 0 {
				sb.WriteString("### Attributes\n\n")
				writeConstraintMap(&sb, doc.Attributes)
				sb.WriteByte('\n')
			}
		}
	}

	if !found {
		return mcpError(fmt.Sprintf(
			"No constraints found for document type %q. "+
				"Use elephant_schema_overview to see available types.",
			input.DocumentType,
		)), nil, nil
	}

	return mcpText(sb.String()), nil, nil
}

func handleResolveRef(
	ctx context.Context, e *mcpEnv, input resolveRefInput,
) (*mcp.CallToolResult, any, error) {
	if input.Ref == "" {
		return mcpError("ref is required"), nil, nil
	}

	schemas, err := e.schemas.get(ctx, e)
	if err != nil {
		return mcpError(fmt.Sprintf("load schemas: %v", err)), nil, nil
	}

	type kindDefs struct {
		kind string
		defs []*revisor.BlockDefinition
	}

	for _, s := range schemas {
		groups := []kindDefs{
			{"link", s.Spec.Links},
			{"meta", s.Spec.Meta},
			{"content", s.Spec.Content},
		}

		for _, g := range groups {
			if input.Kind != "" && input.Kind != g.kind {
				continue
			}

			for _, def := range g.defs {
				if def.ID != input.Ref {
					continue
				}

				var sb strings.Builder

				fmt.Fprintf(&sb, "## %s\n", def.ID)
				fmt.Fprintf(&sb, "Kind: %s\nSchema: %s\n\n",
					g.kind, s.Name)

				writeBlockConstraint(&sb, &def.Block, "")

				return mcpText(sb.String()), nil, nil
			}
		}
	}

	return mcpError(fmt.Sprintf(
		"Block ref %q not found. Check the ref URI and optionally "+
			"specify kind ('link', 'meta', or 'content').",
		input.Ref,
	)), nil, nil
}

func handleEnumValues(
	ctx context.Context, e *mcpEnv, input enumValuesInput,
) (*mcp.CallToolResult, any, error) {
	if input.Ref == "" {
		return mcpError("ref is required"), nil, nil
	}

	schemas, err := e.schemas.get(ctx, e)
	if err != nil {
		return mcpError(fmt.Sprintf("load schemas: %v", err)), nil, nil
	}

	var (
		declares        *revisor.Enum
		declaringSchema string
		extensions      []enumExtension
	)

	for si, s := range schemas {
		for ei := range s.Spec.Enums {
			en := &schemas[si].Spec.Enums[ei]

			if en.Declare == input.Ref {
				declares = en
				declaringSchema = s.Name
			} else if en.Match == input.Ref {
				extensions = append(extensions, enumExtension{
					enum:   en,
					schema: s.Name,
				})
			}
		}
	}

	if declares == nil {
		return mcpError(fmt.Sprintf(
			"Enum %q not found", input.Ref)), nil, nil
	}

	var sb strings.Builder

	fmt.Fprintf(&sb, "## %s\n", input.Ref)

	if declares.Name != "" {
		fmt.Fprintf(&sb, "Name: %s\n", declares.Name)
	}

	if declares.Description != "" {
		fmt.Fprintf(&sb, "Description: %s\n", declares.Description)
	}

	fmt.Fprintf(&sb, "Declared in: %s\n\n### Values\n\n", declaringSchema)

	type valueEntry struct {
		value  string
		status string
	}

	var entries []valueEntry

	for v, c := range declares.Values {
		status := formatEnumStatus(c, "")
		entries = append(entries, valueEntry{v, status})
	}

	for _, ext := range extensions {
		for v, c := range ext.enum.Values {
			status := formatEnumStatus(c, ext.schema)
			entries = append(entries, valueEntry{v, status})
		}
	}

	slices.SortFunc(entries, func(a, b valueEntry) int {
		return strings.Compare(a.value, b.value)
	})

	for _, e := range entries {
		fmt.Fprintf(&sb, "- `%s`%s\n", e.value, e.status)
	}

	return mcpText(sb.String()), nil, nil
}

func handleIndexMappings(
	ctx context.Context, e *mcpEnv, input indexMappingsInput,
) (*mcp.CallToolResult, any, error) {
	if input.DocumentType == "" {
		return mcpError("document_type is required"), nil, nil
	}

	m := apicli.FindMethod("index.SearchV1", "GetMappings")
	if m == nil {
		return mcpError("SearchV1.GetMappings not found in registry"), nil, nil
	}

	endpoint, ok := e.conf.GetEndpoint(m.Endpoint)
	if !ok {
		return mcpError(fmt.Sprintf(
			"no endpoint configured for %q", m.Endpoint)), nil, nil
	}

	reqJSON := fmt.Sprintf(`{"documentType":%q}`, input.DocumentType)

	result, err := m.Call(ctx, e.httpClient, endpoint, reqJSON)
	if err != nil {
		return mcpError(fmt.Sprintf("API error: %v", err)), nil, nil
	}

	var resp mappingsResponse

	err = json.Unmarshal([]byte(result), &resp)
	if err != nil {
		return mcpError(fmt.Sprintf(
			"unmarshal mappings: %v", err)), nil, nil
	}

	filter := strings.ToLower(input.Filter)

	var sb strings.Builder

	fmt.Fprintf(&sb, "## Index mappings for %s\n\n", input.DocumentType)

	matched := 0

	for _, p := range resp.Properties {
		if filter != "" && !strings.Contains(
			strings.ToLower(p.Name), filter) {
			continue
		}

		matched++

		fmt.Fprintf(&sb, "- `%s` (%s)", p.Name, p.Type)

		if p.Path != "" {
			fmt.Fprintf(&sb, " → alias for `%s`", p.Path)
		}

		sb.WriteByte('\n')

		for _, f := range p.Fields {
			fmt.Fprintf(&sb, "  - `.%s` (%s)\n", f.Name, f.Type)
		}
	}

	if matched == 0 && filter != "" {
		fmt.Fprintf(&sb, "No fields matching %q found.\n", input.Filter)
	} else {
		fmt.Fprintf(&sb, "\n%d fields", matched)

		if filter != "" {
			fmt.Fprintf(&sb, " matching %q", input.Filter)
		}

		fmt.Fprintf(&sb, " (of %d total)\n", len(resp.Properties))
	}

	return mcpText(sb.String()), nil, nil
}

type mappingsResponse struct {
	Properties []mappingProperty `json:"properties"`
}

type mappingProperty struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Path   string         `json:"path,omitempty"`
	Fields []mappingField `json:"fields,omitempty"`
}

type mappingField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type enumExtension struct {
	enum   *revisor.Enum
	schema string
}

func formatEnumStatus(c revisor.EnumConstraint, fromSchema string) string {
	var parts []string

	if c.Forbidden {
		if fromSchema != "" {
			parts = append(parts, fmt.Sprintf("forbidden by %s", fromSchema))
		} else {
			parts = append(parts, "forbidden")
		}
	} else if fromSchema != "" {
		parts = append(parts, fmt.Sprintf("added by %s", fromSchema))
	}

	if c.Description != "" {
		parts = append(parts, c.Description)
	}

	if len(parts) == 0 {
		return ""
	}

	return " — " + strings.Join(parts, ", ")
}

// --- Helpers ---

func extractMatchType(doc revisor.DocumentConstraint) string {
	sc, ok := doc.Match.Constraints["type"]
	if !ok {
		return ""
	}

	if sc.Const != nil {
		return *sc.Const
	}

	return ""
}

func matchesDocType(doc revisor.DocumentConstraint, docType string) bool {
	if doc.Declares == docType {
		return true
	}

	return extractMatchType(doc) == docType
}

func writeBlockList(
	sb *strings.Builder, kind string, blocks []*revisor.BlockConstraint,
) {
	if len(blocks) == 0 {
		return
	}

	fmt.Fprintf(sb, "### %s\n\n", kind)

	for _, bc := range blocks {
		writeBlockConstraint(sb, bc, "")
	}

	sb.WriteByte('\n')
}

func writeBlockConstraint(
	sb *strings.Builder, bc *revisor.BlockConstraint, indent string,
) {
	if bc.Ref != "" {
		fmt.Fprintf(sb, "%s- **ref** `%s`", indent, bc.Ref)

		if bc.Name != "" {
			fmt.Fprintf(sb, " — %s", bc.Name)
		}

		writeCountConstraint(sb, bc)
		sb.WriteByte('\n')

		return
	}

	if bc.Declares != nil {
		fmt.Fprintf(sb, "%s- **%s**", indent, formatSignature(bc.Declares))
	} else {
		fmt.Fprintf(sb, "%s- (match)", indent)
	}

	if bc.Name != "" {
		fmt.Fprintf(sb, " — %s", bc.Name)
	}

	writeCountConstraint(sb, bc)
	sb.WriteByte('\n')

	if bc.Description != "" {
		fmt.Fprintf(sb, "%s  %s\n", indent, bc.Description)
	}

	if len(bc.Attributes.Keys) > 0 {
		fmt.Fprintf(sb, "%s  Attributes:\n", indent)

		for _, k := range bc.Attributes.Keys {
			sc := bc.Attributes.Constraints[k]
			fmt.Fprintf(sb, "%s    - `%s`%s\n",
				indent, k, formatStringConstraint(sc))
		}
	}

	if len(bc.Data.Keys) > 0 {
		fmt.Fprintf(sb, "%s  Data:\n", indent)

		for _, k := range bc.Data.Keys {
			sc := bc.Data.Constraints[k]
			fmt.Fprintf(sb, "%s    - `%s`%s\n",
				indent, k, formatStringConstraint(sc))
		}
	}

	for _, nested := range bc.Links {
		writeBlockConstraint(sb, nested, indent+"  ")
	}

	for _, nested := range bc.Meta {
		writeBlockConstraint(sb, nested, indent+"  ")
	}

	for _, nested := range bc.Content {
		writeBlockConstraint(sb, nested, indent+"  ")
	}
}

func writeCountConstraint(sb *strings.Builder, bc *revisor.BlockConstraint) {
	switch {
	case bc.Count != nil:
		fmt.Fprintf(sb, " [exactly %d]", *bc.Count)
	case bc.MinCount != nil && bc.MaxCount != nil:
		fmt.Fprintf(sb, " [%d–%d]", *bc.MinCount, *bc.MaxCount)
	case bc.MaxCount != nil:
		fmt.Fprintf(sb, " [max %d]", *bc.MaxCount)
	case bc.MinCount != nil:
		fmt.Fprintf(sb, " [min %d]", *bc.MinCount)
	}
}

func formatSignature(sig *revisor.BlockSignature) string {
	var parts []string

	if sig.Type != "" {
		parts = append(parts, sig.Type)
	}

	if sig.Rel != "" {
		parts = append(parts, "rel="+sig.Rel)
	}

	if sig.Role != "" {
		parts = append(parts, "role="+sig.Role)
	}

	return strings.Join(parts, ", ")
}

func formatStringConstraint(sc revisor.StringConstraint) string {
	var parts []string

	if sc.Const != nil {
		parts = append(parts, fmt.Sprintf("= %q", *sc.Const))
	}

	if len(sc.Enum) > 0 {
		parts = append(parts, fmt.Sprintf("enum [%s]",
			strings.Join(sc.Enum, ", ")))
	}

	if sc.EnumRef != "" {
		parts = append(parts, "enumRef "+sc.EnumRef)
	}

	if sc.Format != "" {
		parts = append(parts, "format: "+string(sc.Format))
	}

	if len(sc.Glob) > 0 {
		parts = append(parts, "glob: "+sc.Glob.String())
	}

	if sc.Optional {
		parts = append(parts, "optional")
	}

	if sc.AllowEmpty {
		parts = append(parts, "allowEmpty")
	}

	if len(parts) == 0 {
		return ""
	}

	return " (" + strings.Join(parts, ", ") + ")"
}

func writeConstraintMap(sb *strings.Builder, cm revisor.ConstraintMap) {
	for _, k := range cm.Keys {
		sc := cm.Constraints[k]
		fmt.Fprintf(sb, "- `%s`%s\n", k, formatStringConstraint(sc))
	}
}
