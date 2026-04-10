package main

import (
	"bytes"
	"embed"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
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
	CLIName      string
	FuncPrefix   string
	Methods      []methodTmplData
}

type methodTmplData struct {
	GoName       string
	CLIName      string
	InputType    string
	OutputType   string
	AllFlaggable bool
	Fields       []fieldTmplData
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
	}).ParseFS(templateFS, "templates/*.tmpl")
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse templates: %v\n", err)
		os.Exit(1)
	}

	var allServices []serviceTmplData

	for _, cfg := range serviceConfigs {
		fd, err := protoregistry.GlobalFiles.FindFileByPath(cfg.protoFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "find proto file %s: %v\n", cfg.protoFile, err)
			os.Exit(1)
		}

		services := fd.Services()

		for i := range services.Len() {
			sd := services.Get(i)
			svc := processService(cfg, sd)
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

func processService(cfg serviceConfig, sd protoreflect.ServiceDescriptor) serviceTmplData {
	serviceName := string(sd.Name())

	svc := serviceTmplData{
		GoImportPath: cfg.goImportPath,
		GoAlias:      cfg.goAlias,
		Endpoint:     cfg.endpoint,
		ServiceName:  serviceName,
		CLIName:      camelToKebab(serviceName),
		FuncPrefix:   unexport(serviceName),
	}

	methods := sd.Methods()

	for i := range methods.Len() {
		md := methods.Get(i)
		svc.Methods = append(svc.Methods, processMethod(cfg, md))
	}

	return svc
}

func processMethod(cfg serviceConfig, md protoreflect.MethodDescriptor) methodTmplData {
	methodName := string(md.Name())
	inputDesc := md.Input()
	outputDesc := md.Output()

	m := methodTmplData{
		GoName:     methodName,
		CLIName:    camelToKebab(methodName),
		InputType:  string(inputDesc.Name()),
		OutputType: string(outputDesc.Name()),
	}

	fields := inputDesc.Fields()
	allFlaggable := true

	for i := range fields.Len() {
		fd := fields.Get(i)
		f := processField(cfg, fd)
		m.Fields = append(m.Fields, f)

		if !f.FlagEligible {
			allFlaggable = false
		}
	}

	m.AllFlaggable = allFlaggable

	return m
}

func processField(cfg serviceConfig, fd protoreflect.FieldDescriptor) fieldTmplData {
	protoName := string(fd.Name())
	flagName := snakeToKebab(protoName)
	goName := snakeToGoName(protoName)

	f := fieldTmplData{
		FlagName: flagName,
	}

	// Handle maps.
	if fd.IsMap() {
		keyKind := fd.MapKey().Kind()
		valKind := fd.MapValue().Kind()

		if keyKind == protoreflect.StringKind && valKind == protoreflect.StringKind {
			f.FlagEligible = true
			f.FlagType = "StringMapFlag"
			f.FlagDef = stringMapFlagDef(flagName)
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
			f.FlagDef = stringSliceFlagDef(flagName)
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
		f.FlagDef = simpleFlagDef("StringFlag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = cmd.String(%q)", goName, flagName)

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		f.FlagEligible = true
		f.FlagType = "Int64Flag"
		f.FlagDef = simpleFlagDef("Int64Flag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Int64(%q)", goName, flagName)

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		f.FlagEligible = true
		f.FlagType = "Int64Flag"
		f.FlagDef = simpleFlagDef("Int64Flag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = int32(cmd.Int64(%q))", goName, flagName)

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		f.FlagEligible = true
		f.FlagType = "Uint64Flag"
		f.FlagDef = simpleFlagDef("Uint64Flag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Uint64(%q)", goName, flagName)

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		f.FlagEligible = true
		f.FlagType = "Uint64Flag"
		f.FlagDef = simpleFlagDef("Uint64Flag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = uint32(cmd.Uint64(%q))", goName, flagName)

	case protoreflect.BoolKind:
		f.FlagEligible = true
		f.FlagType = "BoolFlag"
		f.FlagDef = simpleFlagDef("BoolFlag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Bool(%q)", goName, flagName)

	case protoreflect.DoubleKind:
		f.FlagEligible = true
		f.FlagType = "FloatFlag"
		f.FlagDef = simpleFlagDef("FloatFlag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = cmd.Float(%q)", goName, flagName)

	case protoreflect.FloatKind:
		f.FlagEligible = true
		f.FlagType = "FloatFlag"
		f.FlagDef = simpleFlagDef("FloatFlag", flagName, "")
		f.AssignCode = fmt.Sprintf("req.%s = float32(cmd.Float(%q))", goName, flagName)

	case protoreflect.EnumKind:
		f.FlagEligible = true
		f.FlagType = "StringFlag"

		enumDesc := fd.Enum()
		enumGoType := enumGoType(enumDesc, cfg.goAlias)
		values := enumValueNames(enumDesc)

		f.FlagDef = simpleFlagDef("StringFlag", flagName,
			fmt.Sprintf("values: %s", strings.Join(values, ", ")))
		f.AssignCode = enumAssignCode(flagName, goName, enumGoType, cfg.goAlias, enumDesc)

	default:
		// Message, bytes, group, etc. - not flag eligible.
		f.FlagEligible = false
	}

	return f
}

func simpleFlagDef(flagType, name, usage string) string {
	if usage != "" {
		return fmt.Sprintf("&cli.%s{Name: %q, Usage: %q}", flagType, name, usage)
	}

	return fmt.Sprintf("&cli.%s{Name: %q}", flagType, name)
}

func stringSliceFlagDef(name string) string {
	return fmt.Sprintf("&cli.StringSliceFlag{Name: %q}", name)
}

func stringMapFlagDef(name string) string {
	return fmt.Sprintf("&cli.StringMapFlag{Name: %q}", name)
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
