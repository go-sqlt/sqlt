# A Go Template-Based SQL Builder and Struct Mapper

[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/go-sqlt/sqlt)
[![GitHub tag (latest SemVer)](https://img.shields.io/github/tag/go-sqlt/sqlt.svg?style=social)](https://github.com/go-sqlt/sqlt/tags)
[![Coverage](https://img.shields.io/badge/Coverage-62.0%25-yellow)](https://github.com/go-sqlt/sqlt/actions)

```go
go get -u github.com/go-sqlt/sqlt
```

`sqlt` uses Go’s template engine to create a flexible, powerful, and type-safe SQL builder and struct mapper.  

Struct mapping is handled by the [structscan](https://pkg.go.dev/github.com/go-sqlt/structscan) package. The `Scan` function returns a `structscan.Schema[Dest]`, which provides a fluent API for field-based value extraction and transformation.

## Example

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"github.com/go-sqlt/sqlt"
	_ "modernc.org/sqlite"
)

type Data struct {
	Int    int64
	String string
	Bool   bool
	Time   time.Time
	Big    *big.Int
	URL    *url.URL
	Slice  []string
	JSON   map[string]any
}

var query = sqlt.All[string, Data](sqlt.Parse(`
	SELECT
		100                                    {{ Scan.Int.To "Int" }}
		, NULL                                 {{ Scan.Nullable.String.To "String" }}
		, true                                 {{ Scan.Bool.To "Bool" }}
		, {{ . }}                              {{ (Scan.String.Time DateOnly).To "Time" }}
		, '300'                                {{ Scan.Text.To "Big" }}
		, 'https://example.com/path?query=yes' {{ Scan.Binary.To "URL" }}
		, 'hello,world'                        {{ (Scan.String.Split ",").To "Slice" }}
		, '{"hello":"world"}'                  {{ Scan.JSON.To "JSON" }}
`))

func main() {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}

	data, err := query.Exec(context.Background(), db, time.Now().Format(time.DateOnly))
	if err != nil {
		panic(err)
	}

	fmt.Println(data) // [{100 default true 2025-05-22 00:00:00 +0000 UTC 300 https://example.com/path?query=yes [hello world] map[hello:world]}]
}
```