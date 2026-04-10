package main

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	_ "github.com/ttab/elephant-api/index"
	_ "github.com/ttab/elephant-api/replicant"
	_ "github.com/ttab/elephant-api/repository"
	_ "github.com/ttab/elephant-api/spell"
	_ "github.com/ttab/elephant-api/user"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

type serviceConfig struct {
	protoFile    string
	goImportPath string
	goAlias      string
	endpoint     string
}

var serviceConfigs = []serviceConfig{
	{
		protoFile:    "repository/service.proto",
		goImportPath: "github.com/ttab/elephant-api/repository",
		goAlias:      "repository",
		endpoint:     "repository",
	},
	{
		protoFile:    "index/service.proto",
		goImportPath: "github.com/ttab/elephant-api/index",
		goAlias:      "index",
		endpoint:     "index",
	},
	{
		protoFile:    "user/service.proto",
		goImportPath: "github.com/ttab/elephant-api/user",
		goAlias:      "user",
		endpoint:     "user",
	},
	{
		protoFile:    "spell/service.proto",
		goImportPath: "github.com/ttab/elephant-api/spell",
		goAlias:      "spell",
		endpoint:     "spell",
	},
	{
		protoFile:    "replicant/service.proto",
		goImportPath: "github.com/ttab/elephant-api/replicant",
		goAlias:      "replicant",
		endpoint:     "replicant",
	},
}

// Template data structures.

type serviceTmplData struct {
	GoImportPath string
	GoAlias      string
	Endpoint     string
	ServiceName  string
	ServiceUsage string
	CLIName      string
	FuncPrefix   string
	Methods      []methodTmplData
}

type methodTmplData struct {
	GoName       string
	CLIName      string
	Usage        string
	InputType    string
	OutputType   string
	AllFlaggable bool
	Fields       []fieldTmplData
	ReqSchema    string // pretty-printed JSON Schema
	ResSchema    string // pretty-printed JSON Schema
}

type fieldTmplData struct {
	FlagName    string
	FlagType    string
	Usage       string
	AssignCode  string
	FlagDef     string
	FlagEligible bool
}

type commandsTmplData struct {
	Services []serviceTmplData
}

func main() {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"join": strings.Join,
		"backtickLit": func(s string) string {
			return "`" + s + "`"
		},
	}).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse templates: %v\n", err)
		os.Exit(1)
	}

	apiDir, err := findModuleDir("github.com/ttab/elephant-api")
	if err != nil {
		fmt.Fprintf(os.Stderr, "find elephant-api module: %v\n", err)
		os.Exit(1)
	}

	var allServices []serviceTmplData

	for _, cfg := range serviceConfigs {
		fd, err := protoregistry.GlobalFiles.FindFileByPath(cfg.protoFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "find proto file %s: %v\n", cfg.protoFile, err)
			os.Exit(1)
		}

		comments, err := parseProtoComments(filepath.Join(apiDir, cfg.protoFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse proto comments %s: %v\n", cfg.protoFile, err)
			os.Exit(1)
		}

		services := fd.Services()

		for i := range services.Len() {
			sd := services.Get(i)
			svc := processService(cfg, sd, comments)
			allServices = append(allServices, svc)

			if err := renderFile(tmpl, "service.go.tmpl", svc,
				fmt.Sprintf("gen_%s.go", strings.ToLower(string(sd.Name())))); err != nil {
				fmt.Fprintf(os.Stderr, "render service %s: %v\n", sd.Name(), err)
				os.Exit(1)
			}
		}
	}

	if err := renderFile(tmpl, "commands.go.tmpl",
		commandsTmplData{Services: allServices}, "commands.go"); err != nil {
		fmt.Fprintf(os.Stderr, "render commands: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "generated %d service command files + commands.go\n", len(allServices))
}

func renderFile(tmpl *template.Template, name string, data any, outFile string) error {
	var buf bytes.Buffer

	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("execute template %s: %w", name, err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		// Write unformatted for debugging.
		_ = os.WriteFile(outFile+".bad", buf.Bytes(), 0o644)

		return fmt.Errorf("format %s: %w", outFile, err)
	}

	outPath := filepath.Join(".", outFile)

	if err := os.WriteFile(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	return nil
}

func processService(cfg serviceConfig, sd protoreflect.ServiceDescriptor, comments commentMap) serviceTmplData {
	serviceName := string(sd.Name())

	svc := serviceTmplData{
		GoImportPath: cfg.goImportPath,
		GoAlias:      cfg.goAlias,
		Endpoint:     cfg.endpoint,
		ServiceName:  serviceName,
		ServiceUsage: comments.get(serviceName),
		CLIName:      camelToKebab(serviceName),
		FuncPrefix:   unexport(serviceName),
	}

	methods := sd.Methods()

	for i := range methods.Len() {
		md := methods.Get(i)
		svc.Methods = append(svc.Methods, processMethod(cfg, sd, md, comments))
	}

	return svc
}

func processMethod(cfg serviceConfig, sd protoreflect.ServiceDescriptor, md protoreflect.MethodDescriptor, comments commentMap) methodTmplData {
	methodName := string(md.Name())
	inputDesc := md.Input()
	outputDesc := md.Output()

	m := methodTmplData{
		GoName:     methodName,
		CLIName:    camelToKebab(methodName),
		Usage:      comments.get(string(sd.Name()) + "." + methodName),
		InputType:  string(inputDesc.Name()),
		OutputType: string(outputDesc.Name()),
	}

	fields := inputDesc.Fields()
	allFlaggable := true

	for i := range fields.Len() {
		fd := fields.Get(i)
		f := processField(cfg, fd, comments)
		m.Fields = append(m.Fields, f)

		if !f.FlagEligible {
			allFlaggable = false
		}
	}

	m.AllFlaggable = allFlaggable
	m.ReqSchema = buildSchemaJSON(inputDesc, comments)
	m.ResSchema = buildSchemaJSON(outputDesc, comments)

	return m
}

func processField(cfg serviceConfig, fd protoreflect.FieldDescriptor, comments commentMap) fieldTmplData {
	protoName := string(fd.Name())
	flagName := snakeToKebab(protoName)
	goName := snakeToGoName(protoName)
	msgName := string(fd.Parent().Name())
	usage := comments.get(msgName + "." + protoName)

	f := fieldTmplData{
		FlagName: flagName,
		Usage:    usage,
	}

	// Handle maps.
	if fd.IsMap() {
		keyKind := fd.MapKey().Kind()
		valKind := fd.MapValue().Kind()

		if keyKind == protoreflect.StringKind && valKind == protoreflect.StringKind {
			f.FlagEligible = true
			f.FlagType = "StringMapFlag"
			f.FlagDef = flagDef("StringMapFlag", flagName, usage)
			f.AssignCode = fmt.Sprintf(
				"if v := cmd.StringMap(%q); len(v) > 0 {\n\treq.%s = v\n}",
				flagName, goName)
		}

		return f
	}

	// Handle repeated fields.
	if fd.IsList() {
		if fd.Kind() == protoreflect.StringKind {
			f.FlagEligible = true
			f.FlagType = "StringSliceFlag"
			f.FlagDef = flagDef("StringSliceFlag", flagName, usage)
			f.AssignCode = fmt.Sprintf(
				"if v := cmd.StringSlice(%q); len(v) > 0 {\n\treq.%s = v\n}",
				flagName, goName)
		}

		return f
	}

	// Handle scalar and enum fields.
	switch fd.Kind() {
	case protoreflect.StringKind:
		f.FlagEligible = true
		f.FlagType = "StringFlag"
		f.FlagDef = flagDef("StringFlag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = cmd.String(%q)", goName, flagName)

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		f.FlagEligible = true
		f.FlagType = "Int64Flag"
		f.FlagDef = flagDef("Int64Flag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Int64(%q)", goName, flagName)

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		f.FlagEligible = true
		f.FlagType = "Int64Flag"
		f.FlagDef = flagDef("Int64Flag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = int32(cmd.Int64(%q))", goName, flagName)

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		f.FlagEligible = true
		f.FlagType = "Uint64Flag"
		f.FlagDef = flagDef("Uint64Flag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Uint64(%q)", goName, flagName)

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		f.FlagEligible = true
		f.FlagType = "Uint64Flag"
		f.FlagDef = flagDef("Uint64Flag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = uint32(cmd.Uint64(%q))", goName, flagName)

	case protoreflect.BoolKind:
		f.FlagEligible = true
		f.FlagType = "BoolFlag"
		f.FlagDef = flagDef("BoolFlag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Bool(%q)", goName, flagName)

	case protoreflect.DoubleKind:
		f.FlagEligible = true
		f.FlagType = "FloatFlag"
		f.FlagDef = flagDef("FloatFlag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Float(%q)", goName, flagName)

	case protoreflect.FloatKind:
		f.FlagEligible = true
		f.FlagType = "FloatFlag"
		f.FlagDef = flagDef("FloatFlag", flagName, usage)
		f.AssignCode = fmt.Sprintf("req.%s = float32(cmd.Float(%q))", goName, flagName)

	case protoreflect.EnumKind:
		f.FlagEligible = true
		f.FlagType = "StringFlag"

		enumDesc := fd.Enum()
		enumGoType := enumGoType(enumDesc, cfg.goAlias)
		values := enumValueNames(enumDesc)

		enumUsage := fmt.Sprintf("values: %s", strings.Join(values, ", "))
		if usage != "" {
			enumUsage = usage + " (" + enumUsage + ")"
		}

		f.FlagDef = flagDef("StringFlag", flagName, enumUsage)
		f.AssignCode = enumAssignCode(flagName, goName, enumGoType, cfg.goAlias, enumDesc)

	default:
		// Message, bytes, group, etc. - not flag eligible.
		f.FlagEligible = false
	}

	return f
}

func flagDef(flagType, name, usage string) string {
	if usage != "" {
		return fmt.Sprintf("&cli.%s{Name: %q, Usage: %q}", flagType, name, usage)
	}

	return fmt.Sprintf("&cli.%s{Name: %q}", flagType, name)
}

func enumGoType(ed protoreflect.EnumDescriptor, pkgAlias string) string {
	fullName := string(ed.FullName())
	pkgName := string(ed.ParentFile().Package())
	relativeName := strings.TrimPrefix(fullName, pkgName+".")
	goName := strings.ReplaceAll(relativeName, ".", "_")

	return pkgAlias + "." + goName
}

func enumValueNames(ed protoreflect.EnumDescriptor) []string {
	values := ed.Values()
	names := make([]string, 0, values.Len())

	for i := range values.Len() {
		names = append(names, string(values.Get(i).Name()))
	}

	return names
}

func enumAssignCode(flagName, goFieldName, enumGoType, pkgAlias string, ed protoreflect.EnumDescriptor) string {
	// Derive the enum value map variable name. The protobuf-go generated code
	// uses: var {TypeName}_value = map[string]int32{...}
	// The TypeName includes parent message prefix with underscore.
	fullName := string(ed.FullName())
	pkgName := string(ed.ParentFile().Package())
	relativeName := strings.TrimPrefix(fullName, pkgName+".")
	goTypeName := strings.ReplaceAll(relativeName, ".", "_")
	valueMapExpr := pkgAlias + "." + goTypeName + "_value"

	return fmt.Sprintf(`if v := cmd.String(%q); v != "" {
		enumVal, err := parseEnum(v, %s)
		if err != nil {
			return fmt.Errorf("invalid --%s: %%w", err)
		}

		req.%s = %s(enumVal)
	}`, flagName, valueMapExpr, flagName, goFieldName, enumGoType)
}

// camelToKebab converts CamelCase to kebab-case.
// Examples: GetHistory -> get-history, SearchV1 -> search-v1, BulkGet -> bulk-get
func camelToKebab(s string) string {
	var (
		result []rune
		runes  = []rune(s)
	)

	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				result = append(result, '-')
			} else if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				result = append(result, '-')
			}
		}

		result = append(result, unicode.ToLower(r))
	}

	return string(result)
}

// snakeToKebab converts snake_case to kebab-case.
func snakeToKebab(s string) string {
	return strings.ReplaceAll(s, "_", "-")
}

// snakeToGoName converts a proto snake_case name to Go CamelCase.
// This matches the protoc-gen-go naming convention.
func snakeToGoName(s string) string {
	parts := strings.Split(s, "_")

	var b strings.Builder

	for _, part := range parts {
		if part == "" {
			continue
		}

		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}

	return b.String()
}

// unexport returns the unexported form of a name (lowercase first letter).
func unexport(s string) string {
	if s == "" {
		return s
	}

	return strings.ToLower(s[:1]) + s[1:]
}

// --- JSON Schema generation ---

// buildSchemaJSON generates a JSON Schema for a protobuf message descriptor,
// using protojson field naming conventions and proto comments as descriptions.
func buildSchemaJSON(md protoreflect.MessageDescriptor, comments commentMap) string {
	schema := buildMessageSchema(md, comments, 0)

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return `{"type":"object"}`
	}

	// Replace backticks so the JSON can be embedded in a Go raw string literal.
	return strings.ReplaceAll(string(data), "`", "'")
}

func buildMessageSchema(md protoreflect.MessageDescriptor, comments commentMap, depth int) map[string]any {
	schema := orderedMap(
		"type", "object",
	)

	msgName := string(md.Name())
	if desc := comments.get(msgName); desc != "" {
		schema["description"] = desc
	}

	if depth > 4 {
		return schema
	}

	fields := md.Fields()
	if fields.Len() == 0 {
		return schema
	}

	props := make(map[string]any, fields.Len())

	for i := range fields.Len() {
		fd := fields.Get(i)
		jsonName := fd.JSONName()
		fs := buildFieldSchema(fd, comments, depth)

		// Add description from proto comment.
		parentName := string(fd.Parent().Name())
		protoName := string(fd.Name())

		if desc := comments.get(parentName + "." + protoName); desc != "" {
			fs["description"] = desc
		}

		props[jsonName] = fs
	}

	schema["properties"] = props

	return schema
}

func buildFieldSchema(fd protoreflect.FieldDescriptor, comments commentMap, depth int) map[string]any {
	if fd.IsMap() {
		valSchema := buildScalarOrMsgSchema(fd.MapValue(), comments, depth)

		return orderedMap(
			"type", "object",
			"additionalProperties", valSchema,
		)
	}

	base := buildScalarOrMsgSchema(fd, comments, depth)

	if fd.IsList() {
		return orderedMap(
			"type", "array",
			"items", base,
		)
	}

	return base
}

func buildScalarOrMsgSchema(fd protoreflect.FieldDescriptor, comments commentMap, depth int) map[string]any {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return orderedMap("type", "string")
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return orderedMap("type", "integer")
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return orderedMap("type", "string", "format", "int64")
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return orderedMap("type", "string", "format", "uint64")
	case protoreflect.BoolKind:
		return orderedMap("type", "boolean")
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return orderedMap("type", "number")
	case protoreflect.BytesKind:
		return orderedMap("type", "string", "format", "byte")
	case protoreflect.EnumKind:
		values := enumValueNames(fd.Enum())
		anyVals := make([]any, len(values))

		for i, v := range values {
			anyVals[i] = v
		}

		return map[string]any{"type": "string", "enum": anyVals}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return buildMessageSchema(fd.Message(), comments, depth+1)
	default:
		return orderedMap("type", "string")
	}
}

// orderedMap creates a map from alternating key/value pairs. The resulting map
// doesn't preserve insertion order (Go maps don't), but json.Marshal sorts keys
// alphabetically which is acceptable for schemas.
func orderedMap(kvs ...any) map[string]any {
	m := make(map[string]any, len(kvs)/2)

	for i := 0; i < len(kvs)-1; i += 2 {
		m[kvs[i].(string)] = kvs[i+1]
	}

	return m
}

// --- Proto comment parsing ---

// commentMap maps "Scope.Name" to the associated comment text.
// Examples: "Documents" (service), "Documents.Get" (method),
// "GetDocumentRequest.uuid" (field).
type commentMap map[string]string

func (m commentMap) get(key string) string {
	if m == nil {
		return ""
	}

	return m[key]
}

var (
	serviceRe = regexp.MustCompile(`^\s*service\s+(\w+)`)
	messageRe = regexp.MustCompile(`^\s*message\s+(\w+)`)
	rpcRe     = regexp.MustCompile(`^\s*rpc\s+(\w+)`)
	fieldRe   = regexp.MustCompile(`\s(\w+)\s*=\s*\d+`)
)

// parseProtoComments reads a .proto source file and extracts leading comments
// for services, methods, messages, and fields.
func parseProtoComments(path string) (commentMap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	defer func() { _ = f.Close() }()

	result := make(commentMap)

	var (
		commentBuf []string
		depth      int
		scope      []string // stack of service/message names
	)

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Accumulate comment lines.
		if strings.HasPrefix(trimmed, "//") {
			text := strings.TrimPrefix(trimmed, "//")
			text = strings.TrimPrefix(text, " ")
			commentBuf = append(commentBuf, text)

			continue
		}

		comment := strings.Join(commentBuf, " ")
		commentBuf = nil

		// Skip blank lines and lines that are just braces.
		if trimmed == "" || trimmed == "}" || trimmed == "{" {
			// Adjust depth for lone braces.
			depth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")

			for len(scope) > depth {
				scope = scope[:len(scope)-1]
			}

			continue
		}

		// Detect declarations.
		switch {
		case serviceRe.MatchString(trimmed):
			name := serviceRe.FindStringSubmatch(trimmed)[1]
			if comment != "" {
				result[name] = comment
			}

			scope = append(scope, name)
		case messageRe.MatchString(trimmed):
			name := messageRe.FindStringSubmatch(trimmed)[1]
			if comment != "" {
				result[name] = comment
			}

			scope = append(scope, name)
		case rpcRe.MatchString(trimmed):
			name := rpcRe.FindStringSubmatch(trimmed)[1]
			if comment != "" && len(scope) > 0 {
				result[scope[len(scope)-1]+"."+name] = comment
			}
		case fieldRe.MatchString(trimmed):
			m := fieldRe.FindStringSubmatch(trimmed)
			name := m[1]

			if comment != "" && len(scope) > 0 {
				result[scope[len(scope)-1]+"."+name] = comment
			}
		}

		// Update brace depth.
		depth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")

		for len(scope) > depth {
			scope = scope[:len(scope)-1]
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}

	return result, nil
}

// findModuleDir returns the on-disk directory for a Go module dependency.
func findModuleDir(modulePath string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-json", modulePath)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list -m -json %s: %w", modulePath, err)
	}

	var info struct {
		Dir string
	}

	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("parse module info: %w", err)
	}

	if info.Dir == "" {
		return "", fmt.Errorf("module %s has no local directory", modulePath)
	}

	return info.Dir, nil
}
