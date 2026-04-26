# PLAN: tinywasm/depfind — Fallback cuando `go list` falla en entorno de test

## Contexto

`GoDepFind` se usa para determinar si un archivo `.go` es dependencia del archivo principal
de un handler (`ThisFileIsMine`). La función inicializa un caché mediante `rebuildCache`,
que ejecuta `go list ./...` en el directorio raíz del proyecto.

## Problema

Cuando `go list ./...` falla (dependencias externas no disponibles en caché local, entorno
aislado, módulo recién creado sin `go.sum` completo), `rebuildCache` retorna error →
`ensureCacheInitialized` propaga el error → `updateCacheForFileWithContext` retorna error →
`thisFileIsMine` retorna `(false, error)` sin llegar a `checkPackageBasedOwnership`.

Resultado: el handler nunca recibe el evento de archivo aunque el archivo esté físicamente
en el mismo proyecto.

### Síntoma observable (ejemplo de llamador)
```
WASM compilations triggered: 0   (esperado: ≥1)
Browser reloads triggered: 2     (el browser-reload ocurre por otra vía)
```

## Diagnóstico

Flujo en `thisFileIsMine`:
```
fileAbsPath = /tmp/.../pkg/greet/greet.go
mainInputFileRelativePath = web/client.go
rootDirs = [/tmp/...]

→ isSubpath = true (greet.go SÍ está bajo rootDir)
→ updateCacheForFileWithContext → ensureCacheInitialized → rebuildCache
→ rebuildCache: go list ./... falla (módulo externo no en caché)
→ return error → thisFileIsMine return (false, error)
→ devwatch: "continue" en el error → handler no recibe evento
```

## Solución

El error se produce en `updateCacheForFileWithContext` (que llama `ensureCacheInitialized`)
**antes** de llegar a `checkPackageBasedOwnership`. La corrección debe hacerse en dos
lugares en el orden correcto: primero silenciar el error de caché, luego agregar el fallback.

### Paso 1 — No propagar error de `rebuildCache` como fatal en `ensureCacheInitialized`

Cuando `rebuildCache` falla, el caché queda no inicializado (`cachedModule = false`).
En la siguiente llamada intentará de nuevo — costoso si falla siempre. Marcar como
inicializado con caché vacío para evitar re-intentos constantes, y dejar que el fallback
del Paso 2 actúe.

**Archivo**: `cache.go` — función `ensureCacheInitialized`

```go
func (g *GoDepFind) ensureCacheInitialized() error {
    if !g.cachedModule {
        err := g.rebuildCache()
        // Marcar inicializado aunque falle para evitar re-intentos en cada evento
        g.cachedModule = true
        if err != nil {
            // Inicializar mapas vacíos para que los lookups no paniquen
            if g.packageCache == nil {
                g.packageCache = make(map[string]*build.Package)
            }
            if g.filePathToPackage == nil {
                g.filePathToPackage = make(map[string]string)
            }
            if g.fileToPackages == nil {
                g.fileToPackages = make(map[string][]string)
            }
            // No retornar el error — el fallback en checkPackageBasedOwnership
            // manejará la ausencia de caché
            return nil
        }
    }
    return nil
}
```

> **Nota**: Si `rebuildCache` falla por error transitorio, el caché nunca se construye.
> Para entornos de producción donde `go list` siempre funciona, esto no cambia el comportamiento.
> Para re-intentar la construcción del caché cuando el módulo esté disponible, se puede
> exponer un método `InvalidateCache()` que resetea `cachedModule = false`.

### Paso 2 — Fallback por presencia física en `checkPackageBasedOwnership`

Con el Paso 1, `ensureCacheInitialized` ya no propaga error, por lo que
`updateCacheForFileWithContext` completa y el flujo llega a `checkPackageBasedOwnership`.
Allí, cuando el caché está vacío (`targetPkg == ""`), agregar fallback por directorio:
si el `mainInputFile` del handler existe bajo algún rootDir y el archivo también está
bajo ese mismo rootDir, asumir que pertenece.

**Archivo**: `godepfind.go` — función `checkPackageBasedOwnership`

```go
func (g *GoDepFind) checkPackageBasedOwnership(mainInputFileRelativePath, fileAbsPath string) (bool, error) {
    targetPkg, err := g.findPackageForFile(fileAbsPath)
    if err != nil {
        return false, err
    }

    // Fallback: caché vacío (go list falló), pero el archivo está bajo un rootDir
    // donde el handler también existe → asumir que pertenece
    if targetPkg == "" {
        for _, root := range g.rootDirs {
            handlerMainAbs := filepath.Join(root, mainInputFileRelativePath)
            if _, statErr := os.Stat(handlerMainAbs); statErr == nil {
                if strings.HasPrefix(fileAbsPath, root+string(filepath.Separator)) {
                    return true, nil
                }
            }
        }
        return false, nil
    }

    return g.doesPackageBelongToHandler(targetPkg, mainInputFileRelativePath), nil
}
```

### Paso 3 — Test de regresión

Agregar test en `validation_test.go` o nuevo archivo `fallback_test.go`:

```go
func TestThisFileIsMine_GoListFails(t *testing.T) {
    // Crear proyecto mínimo sin dependencias externas (go list puede ejecutarse)
    // Pero simular falla de go list usando un directorio sin go.mod para rebuildCache
    tmp := t.TempDir()
    
    // Crear estructura: main.go + pkg/dep/dep.go sin go.mod
    os.MkdirAll(filepath.Join(tmp, "pkg/dep"), 0755)
    os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\nfunc main(){}"), 0644)
    os.WriteFile(filepath.Join(tmp, "pkg/dep/dep.go"), []byte("package dep"), 0644)
    
    // go list fallará porque no hay go.mod
    finder := New(tmp)
    
    isMine, err := finder.ThisFileIsMine("main.go", filepath.Join(tmp, "pkg/dep/dep.go"), "write")
    
    // No debe retornar error fatal
    if err != nil {
        t.Errorf("Unexpected error: %v", err)
    }
    // Fallback: el archivo está bajo rootDir y main.go existe → debe ser true
    if !isMine {
        t.Error("Expected isMine=true via fallback when go list fails")
    }
}
```

---

## Stage 2 — Limpiar salida de tests

### Problema

Los tests emiten logs informativos en ejecuciones normales (sin `-v`), lo que ensucia la
salida de `gotest` aunque todos los tests pasen. `t.Logf` en Go **solo imprime si el test
falla o si se pasa `-v`** — pero varios tests tienen logs incondicionales de estado interno
que aparecen siempre porque el test termina en `t.Skip` (los tests skipped sí imprimen
sus logs acumulados).

Dos categorías de ruido:

**A — Logs de diagnóstico interno** (aparecen en runs normales porque el test falla o skippea):
- `main_dependency_test.go`: 8 `t.Logf` de estado del caché (`All packages found`, `Packages loaded`, etc.)
- `cache_test.go`: 9 `t.Logf` de diagnóstico de inicialización
- `mainfile_differentiation_test.go`: `t.Logf` de diagnóstico en subtests que hacen skip
- `real_world_test.go`: 16 `t.Logf` en tests que terminan en skip
- `production_issue_test.go`: 13 `t.Logf` de estado paso a paso
- `dynamic_debug_test.go`: 32 `t.Logf` (el archivo de mayor ruido)
- `routing_issue_unit_test.go`: 16 `t.Logf`

**B — Tests que hacen skip por rutas relativas hardcodeadas**:
`mainfile_differentiation_test.go` y `mainfile_ownership_test.go` intentan acceder a rutas
relativas como `appAserver/main.go` (sin `t.TempDir()` ni paths absolutos), que no existen
en el CWD del runner de tests → todos sus subtests hacen skip y emiten el mensaje de skip.

### Corrección

#### B.1 — Agregar helper `logf` en un archivo `helpers_test.go` nuevo

```go
package depfind

import "testing"

// logf imprime solo cuando el test falla o se ejecuta con -v.
// Usar en lugar de t.Logf para logs de diagnóstico interno.
func logf(t *testing.T, format string, args ...any) {
    t.Helper()
    if testing.Verbose() {
        t.Logf(format, args...)
    }
}
```

Reemplazar todos los `t.Logf(...)` de diagnóstico interno (no los que preceden a un
`t.Errorf` o `t.Fatalf`) por `logf(t, ...)` en los archivos con mayor ruido:
`main_dependency_test.go`, `cache_test.go`, `dynamic_debug_test.go`,
`production_issue_test.go`, `routing_issue_unit_test.go`, `real_world_test.go`.

> `t.Log`/`t.Logf` en Go ya son silenciosos cuando el test pasa en modo normal.
> El ruido actual viene de tests que terminan en `t.Skip` — los skipped sí vacían
> su buffer de logs. El helper con `testing.Verbose()` silencia también esos casos.

#### B.2 — Corregir tests con rutas relativas hardcodeadas

`mainfile_differentiation_test.go` y `mainfile_ownership_test.go` usan rutas como
`appAserver/main.go` relativas al CWD, que no existen → skip masivo con logs.

Opciones (elegir una por test):
- Si el test ya no es relevante dado el estado actual del código: **eliminar el test**.
- Si sigue siendo relevante: reescribir usando `t.TempDir()` y crear los archivos necesarios
  igual que el resto de los tests del paquete.

### Archivos afectados

| Archivo | Acción |
|---------|--------|
| `helpers_test.go` (nuevo) | Crear helper `logf` |
| `main_dependency_test.go` | Reemplazar `t.Logf` de diagnóstico por `logf` |
| `cache_test.go` | Reemplazar `t.Logf` de diagnóstico por `logf` |
| `dynamic_debug_test.go` | Reemplazar `t.Logf` de diagnóstico por `logf` |
| `production_issue_test.go` | Reemplazar `t.Logf` de diagnóstico por `logf` |
| `routing_issue_unit_test.go` | Reemplazar `t.Logf` de diagnóstico por `logf` |
| `real_world_test.go` | Reemplazar `t.Logf` de diagnóstico por `logf` |
| `mainfile_differentiation_test.go` | Eliminar o reescribir con `t.TempDir()` |
| `mainfile_ownership_test.go` | Eliminar o reescribir con `t.TempDir()` |

### Resultado esperado

```
$ gotest
ok  	github.com/tinywasm/depfind	X.XXs
```

Sin líneas de log intermedias. Con `-v` o en caso de fallo, todos los logs siguen disponibles.

---

## Orden de implementación

| # | Stage | Acción | Archivo |
|---|-------|--------|---------|
| 1 | 1 | `ensureCacheInitialized`: silenciar error de `rebuildCache`, inicializar mapas vacíos | `cache.go` |
| 2 | 1 | `checkPackageBasedOwnership`: fallback por directorio cuando `targetPkg == ""` | `godepfind.go` |
| 3 | 1 | Test de regresión `TestThisFileIsMine_GoListFails` | `fallback_test.go` (nuevo) |
| 4 | 2 | Helper `logf` y reemplazo de `t.Logf` en tests de diagnóstico | `helpers_test.go` + 6 archivos |
| 5 | 2 | Eliminar o reescribir tests con rutas relativas hardcodeadas | `mainfile_differentiation_test.go`, `mainfile_ownership_test.go` |
| 6 | — | Publicar nueva versión del módulo | bump de versión en `go.mod` |

## Impacto y riesgos

- **Falso positivo**: Un archivo bajo el rootDir del handler podría ser tratado como
  "mine" aunque no sea dependencia real. Riesgo aceptable: en ese caso el handler
  compila/procesa el archivo y el resultado es idéntico (ya compilaría de todas formas
  porque están en el mismo proyecto).
- **Sin cambio de API pública**: `ThisFileIsMine` mantiene la misma firma.
- **Backward compatible**: En entornos donde `go list` funciona, el comportamiento
  es idéntico — el fallback solo actúa cuando `targetPkg == ""`.
