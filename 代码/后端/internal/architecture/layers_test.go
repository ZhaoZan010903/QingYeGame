// Package architecture 固定依赖方向，避免后续改动把 HTTP 或驱动类型带回业务层。
package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func under(path, prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }
func driverOrHTTP(path string) bool {
	for _, prefix := range []string{"net/http", "database/sql", "github.com/gin-gonic/gin", "github.com/jackc/pgx", "github.com/redis/go-redis"} {
		if under(path, prefix) {
			return true
		}
	}
	return false
}
func TestLayerDependencies(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture tests")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(source)))
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 || fields[0] != "module" {
		t.Fatal("cannot read module name")
	}
	prefix := fields[1] + "/internal/"
	internal := filepath.Join(root, "internal")
	counts := map[string]int{}
	err = filepath.WalkDir(internal, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(internal, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		role := strings.SplitN(rel, "/", 2)[0]
		counts[role]++
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			dependency := strings.TrimPrefix(imported, prefix)
			local := dependency != imported
			forbidden := ""
			switch role {
			case "domain":
				if driverOrHTTP(imported) {
					forbidden = "领域层不得依赖 HTTP 或数据驱动"
				}
				if local {
					for _, target := range []string{"app", "application", "infrastructure", "transport", "bootstrap"} {
						if under(dependency, target) {
							forbidden = "领域层不得反向依赖外层"
						}
					}
				}
			case "application":
				if driverOrHTTP(imported) {
					forbidden = "应用层必须使用端口，不能依赖 HTTP 或数据驱动"
				}
				if local {
					for _, target := range []string{"app", "infrastructure", "transport", "bootstrap"} {
						if under(dependency, target) {
							forbidden = "应用层不得依赖具体适配器或启动装配"
						}
					}
				}
			case "infrastructure":
				if local {
					for _, target := range []string{"app", "transport", "bootstrap"} {
						if under(dependency, target) {
							forbidden = "基础设施不能反向依赖入口"
						}
					}
				}
			}
			// Gin Context 只能留在 HTTP 适配器，不能进入任务、领域或存储代码。
			if under(imported, "github.com/gin-gonic/gin") && !strings.HasPrefix(rel, "transport/http") {
				forbidden = "Gin 只允许存在于 transport/http* 目录"
			}
			if forbidden != "" {
				t.Errorf("%s: %s: import %s", set.Position(spec.Pos()), forbidden, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"domain", "application", "infrastructure"} {
		if counts[role] == 0 {
			t.Errorf("没有扫描到 %s 层，检查源代码根目录", role)
		}
	}
}
