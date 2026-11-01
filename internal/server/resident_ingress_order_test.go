package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"
)

func controlSourceFunction(t *testing.T, file, name string) *ast.FuncDecl {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 1<<20 {
		t.Fatal("bounded source fixture too large")
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s absent", name)
	return nil
}

func hasControlCall(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == name {
			found = true
		}
		return true
	})
	return found
}

func TestPendingControlGuardStaticallyPrecedesWSSubscriptionsAndGoroutines(t *testing.T) {
	fn := controlSourceFunction(t, "server.go", "ws")
	var loop *ast.ForStmt
	for _, statement := range fn.Body.List {
		candidate, ok := statement.(*ast.ForStmt)
		if !ok {
			continue
		}
		for _, s := range candidate.Body.List {
			declaration, ok := s.(*ast.DeclStmt)
			if !ok {
				continue
			}
			g, ok := declaration.Decl.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, spec := range g.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if ok && len(value.Names) == 1 && value.Names[0].Name == "req" {
					loop = candidate
				}
			}
		}
	}
	if loop == nil {
		t.Fatal("actual original WS request loop absent")
	}
	guard, envelope := -1, -1
	for i, statement := range loop.Body.List {
		if hasControlCall(statement, "guardPendingIngressOperation") {
			if guard != -1 {
				t.Fatal("ambiguous pending guard")
			}
			guard = i
			check, ok := statement.(*ast.IfStmt)
			if !ok || len(check.Body.List) == 0 {
				t.Fatal("guard is not a direct conditional cut")
			}
			last, ok := check.Body.List[len(check.Body.List)-1].(*ast.BranchStmt)
			if !ok || last.Tok != token.CONTINUE {
				t.Fatal("guard refusal can fall through to effects")
			}
		}
		if hasControlCall(statement, "pendingRequestEnvelopeFits") {
			envelope = i
		}
	}
	if envelope < 0 || guard < 0 || envelope >= guard {
		t.Fatal("bounded envelope/original pending cuts absent or reversed")
	}
	wanted := map[string]bool{"mesh_operator_watch": false, "subscribe": false, "monitor_subscribe": false, "monitor_unsubscribe": false}
	for i, statement := range loop.Body.List {
		if branch, ok := statement.(*ast.IfStmt); ok {
			ast.Inspect(branch.Cond, func(n ast.Node) bool {
				v, ok := n.(*ast.BasicLit)
				if !ok || v.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(v.Value)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := wanted[text]; ok {
					if i <= guard {
						t.Fatalf("%s can precede pending guard", text)
					}
					wanted[text] = true
				}
				return true
			})
		}
		ast.Inspect(statement, func(n ast.Node) bool {
			if _, ok := n.(*ast.GoStmt); ok && i <= guard {
				t.Fatal("request goroutine precedes pending guard")
			}
			return true
		})
	}
	for name, present := range wanted {
		if !present {
			t.Fatalf("actual %s branch not covered", name)
		}
	}
}

func TestPendingControlGuardStaticallyPrecedesPreparedCopyAndDomainDispatch(t *testing.T) {
	prepare := controlSourceFunction(t, "resident_dispatch.go", "prepareCall")
	guard, copyIndex := -1, -1
	for i, statement := range prepare.Body.List {
		if hasControlCall(statement, "guardPendingIngressOperation") {
			guard = i
		}
		ast.Inspect(statement, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "append" {
					copyIndex = i
				}
			}
			return true
		})
	}
	if guard < 0 || copyIndex < 0 || guard >= copyIndex {
		t.Fatal("pending guard no longer precedes request copy/registration")
	}
	dispatch := controlSourceFunction(t, "resident_dispatch.go", "dispatchPrepared")
	guard, diagnosis, domain := -1, -1, -1
	for i, statement := range dispatch.Body.List {
		if hasControlCall(statement, "guardPendingIngressOperation") {
			guard = i
		}
		if hasControlCall(statement, "inspectOriginalRuntime") {
			diagnosis = i
			for _, name := range []string{"projectDispatch", "dispatch", "customNamedView", "namedView"} {
				if hasControlCall(statement, name) {
					t.Fatalf("pending diagnosis invokes ordinary domain projection %s", name)
				}
			}
		}
		if hasControlCall(statement, "dispatch") {
			domain = i
		}
	}
	if guard < 0 || diagnosis <= guard || domain <= diagnosis {
		t.Fatal("bounded pending diagnosis can fall through to full domain projection before its guard")
	}
}
