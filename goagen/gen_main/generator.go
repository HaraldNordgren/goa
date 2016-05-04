package genmain

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/goadesign/goa/design"
	"github.com/goadesign/goa/goagen/codegen"
	"github.com/goadesign/goa/goagen/utils"
	"github.com/spf13/cobra"
)

// Generator is the application code generator.
type Generator struct {
	genfiles []string
}

// Generate is the generator entry point called by the meta generator.
func Generate() (files []string, err error) {
	api := design.Design
	if err != nil {
		return nil, err
	}
	g := new(Generator)
	root := &cobra.Command{
		Use:   "goagen",
		Short: "Main generator",
		Long:  "application main generator",
		Run:   func(*cobra.Command, []string) { files, err = g.Generate(api) },
	}
	codegen.RegisterFlags(root)
	NewCommand().RegisterFlags(root)
	root.Execute()
	return
}

// Generate produces the skeleton main.
func (g *Generator) Generate(api *design.APIDefinition) (_ []string, err error) {
	go utils.Catch(nil, func() { g.Cleanup() })

	defer func() {
		if err != nil {
			g.Cleanup()
		}
	}()

	mainFile := filepath.Join(codegen.OutputDir, "main.go")
	if Force {
		os.Remove(mainFile)
	}
	funcs := template.FuncMap{
		"tempvar":         tempvar,
		"generateSwagger": generateSwagger,
		"okResp":          okResp,
		"targetPkg":       func() string { return TargetPackage },
	}
	imp, err := codegen.PackagePath(codegen.OutputDir)
	if err != nil {
		return nil, err
	}
	imp = path.Join(filepath.ToSlash(imp), "app")
	_, err = os.Stat(mainFile)
	if err != nil {
		g.genfiles = append(g.genfiles, mainFile)
		file, err2 := codegen.SourceFileFor(mainFile)
		if err2 != nil {
			return nil, err2
		}
		var outPkg string
		outPkg, err2 = codegen.PackagePath(codegen.OutputDir)
		if err2 != nil {
			return nil, err2
		}
		outPkg = strings.TrimPrefix(filepath.ToSlash(outPkg), "src/")
		appPkg := path.Join(outPkg, "app")
		swaggerPkg := path.Join(outPkg, "swagger")
		imports := []*codegen.ImportSpec{
			codegen.SimpleImport("time"),
			codegen.SimpleImport("github.com/goadesign/goa"),
			codegen.SimpleImport("github.com/goadesign/goa/middleware"),
			codegen.SimpleImport(appPkg),
			codegen.SimpleImport(swaggerPkg),
		}
		if generateSwagger() {
			jsonSchemaPkg := path.Join(outPkg, "schema")
			imports = append(imports, codegen.SimpleImport(jsonSchemaPkg))
		}
		file.WriteHeader("", "main", imports)
		data := map[string]interface{}{
			"Name": AppName,
			"API":  api,
		}
		if err2 = file.ExecuteTemplate("main", mainT, funcs, data); err2 != nil {
			return nil, err2
		}
		if err2 = file.FormatCode(); err2 != nil {
			return nil, err2
		}
	}
	imports := []*codegen.ImportSpec{
		codegen.SimpleImport("io"),
		codegen.SimpleImport("github.com/goadesign/goa"),
		codegen.SimpleImport(imp),
		codegen.SimpleImport("golang.org/x/net/websocket"),
	}
	err = api.IterateResources(func(r *design.ResourceDefinition) error {
		filename := filepath.Join(codegen.OutputDir, codegen.SnakeCase(r.Name)+".go")
		if Force {
			if err2 := os.Remove(filename); err2 != nil {
				return err2
			}
		}
		if _, e := os.Stat(filename); e != nil {
			g.genfiles = append(g.genfiles, filename)
			file, err2 := codegen.SourceFileFor(filename)
			if err2 != nil {
				return err
			}
			file.WriteHeader("", "main", imports)
			if err2 = file.ExecuteTemplate("controller", ctrlT, funcs, r); err2 != nil {
				return err
			}
			err2 = r.IterateActions(func(a *design.ActionDefinition) error {
				if a.WebSocket() {
					return file.ExecuteTemplate("actionWS", actionWST, funcs, a)
				}
				return file.ExecuteTemplate("action", actionT, funcs, a)
			})
			if err2 != nil {
				return err
			}
			if err2 = file.FormatCode(); err2 != nil {
				return err2
			}
		}
		return nil
	})
	if err != nil {
		return
	}

	return g.genfiles, nil
}

// Cleanup removes all the files generated by this generator during the last invokation of Generate.
func (g *Generator) Cleanup() {
	for _, f := range g.genfiles {
		os.Remove(f)
	}
	g.genfiles = nil
}

// tempCount is the counter used to create unique temporary variable names.
var tempCount int

// tempvar generates a unique temp var name.
func tempvar() string {
	tempCount++
	if tempCount == 1 {
		return "c"
	}
	return fmt.Sprintf("c%d", tempCount)
}

// generateSwagger returns true if the API Swagger spec should be generated.
func generateSwagger() bool {
	return codegen.CommandName == "" || codegen.CommandName == "swagger"
}

func okResp(a *design.ActionDefinition) map[string]interface{} {
	var ok *design.ResponseDefinition
	for _, resp := range a.Responses {
		if resp.Status == 200 {
			ok = resp
			break
		}
	}
	if ok == nil {
		return nil
	}
	var mt *design.MediaTypeDefinition
	var ok2 bool
	if mt, ok2 = design.Design.MediaTypes[design.CanonicalIdentifier(ok.MediaType)]; !ok2 {
		return nil
	}
	view := "default"
	if _, ok := mt.Views["default"]; !ok {
		for v := range mt.Views {
			view = v
			break
		}
	}
	pmt, _, err := mt.Project(view)
	if err != nil {
		return nil
	}
	name := codegen.GoTypeRef(pmt, pmt.AllRequired(), 1, false)
	var pointer string
	if strings.HasPrefix(name, "*") {
		name = name[1:]
		pointer = "*"
	}
	typeref := fmt.Sprintf("%s%s.%s", pointer, TargetPackage, name)
	if strings.HasPrefix(typeref, "*") {
		typeref = "&" + typeref[1:]
	}
	var nameSuffix string
	if view != "default" {
		nameSuffix = codegen.Goify(view, true)
	}
	return map[string]interface{}{
		"Name":    ok.Name + nameSuffix,
		"GoType":  codegen.GoNativeType(pmt),
		"TypeRef": typeref,
	}
}

const mainT = `
func main() {
	// Create service
	service := goa.New({{ printf "%q" .Name }})

	// Setup middleware
	service.Use(middleware.RequestID())
	service.Use(middleware.LogRequest(true))
	service.Use(middleware.ErrorHandler(service, true))
	service.Use(middleware.Recover())
{{ $api := .API }}
{{ range $name, $res := $api.Resources }}{{ $name := goify $res.Name true }} // Mount "{{$res.Name}}" controller
	{{ $tmp := tempvar }}{{ $tmp }} := New{{ $name }}Controller(service)
	{{ targetPkg }}.Mount{{ $name }}Controller(service, {{ $tmp }})
{{ end }}{{ if generateSwagger }}// Mount Swagger spec provider controller
	swagger.MountController(service)
{{ end }}

	if err := service.ListenAndServe(":8080"); err != nil {
		service.LogError("startup", "err", err)
	}
}
`

const ctrlT = `// {{ $ctrlName := printf "%s%s" (goify .Name true) "Controller" }}{{ $ctrlName }} implements the {{ .Name }} resource.
type {{ $ctrlName }} struct {
	*goa.Controller
}

// New{{ $ctrlName }} creates a {{ .Name }} controller.
func New{{ $ctrlName }}(service *goa.Service) *{{ $ctrlName }} {
	return &{{ $ctrlName }}{Controller: service.NewController("{{ $ctrlName }}")}
}
`

const actionT = `{{ $ctrlName := printf "%s%s" (goify .Parent.Name true) "Controller" }}// {{ goify .Name true }} runs the {{ .Name }} action.
func (c *{{ $ctrlName }}) {{ goify .Name true }}(ctx *{{ targetPkg }}.{{ goify .Name true }}{{ goify .Parent.Name true }}Context) error {
	// TBD: implement
{{ $ok := okResp . }}{{ if $ok }} res := {{ $ok.TypeRef }}{}
{{ end }} return {{ if $ok }}ctx.{{ $ok.Name }}(res){{ else }}nil{{ end }}
}
`

const actionWST = `{{ $ctrlName := printf "%s%s" (goify .Parent.Name true) "Controller" }}// {{ goify .Name true }} runs the {{ .Name }} action.
func (c *{{ $ctrlName }}) {{ goify .Name true }}(ctx *{{ targetPkg }}.{{ goify .Name true }}{{ goify .Parent.Name true }}Context) error {
	c.{{ goify .Name true }}WSHandler(ctx).ServeHTTP(ctx.ResponseWriter, ctx.Request)
	return nil
}

// {{ goify .Name true }}WSHandler establishes a websocket connection to run the {{ .Name }} action.
func (c *{{ $ctrlName }}) {{ goify .Name true }}WSHandler(ctx *{{ targetPkg }}.{{ goify .Name true }}{{ goify .Parent.Name true }}Context) websocket.Handler {
	return func(ws *websocket.Conn) {
		// TBD: implement
		ws.Write([]byte("{{ .Name }} {{ .Parent.Name }}"))
		// Dummy echo websocket server
		io.Copy(ws, ws)
	}
}
`
