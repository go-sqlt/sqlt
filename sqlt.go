// Package sqlt provides a type-safe SQL builder and struct mapper using Go's text/template engine.
//
// Example:
/*

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

var (
	query = sqlt.All[string, Data](sqlt.Parse(`
		SELECT
			100                                    {{ Dest.Int.Int }}
			, NULL                                 {{ Dest.String.String.Default "default" }}
			, true                                 {{ Dest.Bool.Bool }}
			, {{ . }}                              {{ Dest.Time.ParseTime DateOnly }}
			, '300'                                {{ Dest.Big.UnmarshalText }}
			, 'https://example.com/path?query=yes' {{ Dest.URL.UnmarshalBinary }}
			, 'hello,world'                        {{ Dest.Slice.Split "," }}
			, '{"hello":"world"}'                  {{ Dest.JSON.UnmarshalJSON }}
	`))
)

func main() {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}

	data, err := query.Exec(context.Background(), db, time.Now().Format(time.DateOnly))
	if err != nil {
		panic(err)
	}

	fmt.Println(data)
	// [{100 default true 2025-05-22 00:00:00 +0000 UTC 300 https://example.com/path?query=yes [hello world] map[hello:world]}]
}

*/
package sqlt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/go-sqlt/datahash"
	"github.com/go-sqlt/structscan"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/jba/templatecheck"
)

// DB defines the subset of database operations required by sqlt.
// It is implemented by *sql.DB and *sql.Tx.
type DB interface {
	QueryContext(ctx context.Context, sql string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, sql string, args ...any) *sql.Row
	ExecContext(ctx context.Context, sql string, args ...any) (sql.Result, error)
}

// Config defines options for SQL template parsing and execution.
// Fields are merged; later values override earlier ones.
// Parsers are appended.
type Config struct {
	Dialect              string
	Placeholder          func(pos int, writer io.Writer) error
	Logger               func(ctx context.Context, info Info)
	ExpressionSize       int
	ExpressionExpiration time.Duration
	Hasher               func(value any) (uint64, error)
	Parsers              []func(tpl *template.Template) (*template.Template, error)
}

// With merges the current Config with additional Configs.
// Later fields override earlier ones. Parsers are appended in order.
func (c Config) With(configs ...Config) Config {
	merged := Config{}

	for _, override := range append([]Config{c}, configs...) {
		if override.Dialect != "" {
			merged.Dialect = override.Dialect
		}

		if override.Placeholder != nil {
			merged.Placeholder = override.Placeholder
		}

		if override.Logger != nil {
			merged.Logger = override.Logger
		}

		if override.ExpressionSize != 0 {
			merged.ExpressionSize = override.ExpressionSize
		}

		if override.ExpressionExpiration != 0 {
			merged.ExpressionExpiration = override.ExpressionExpiration
		}

		if override.Hasher != nil {
			merged.Hasher = override.Hasher
		}

		if len(override.Parsers) > 0 {
			merged.Parsers = append(merged.Parsers, override.Parsers...)
		}
	}

	return merged
}

// Logger adds a callback for logging execution metadata per statement.
func Logger(fn func(ctx context.Context, info Info)) Config {
	return Config{
		Logger: fn,
	}
}

// Hasher sets a custom function for hashing parameters (used for caching of expressions).
// Uses datahash by default. Exclude fields with: `datahash:"-"`.
func Hasher(fn func(param any) (uint64, error)) Config {
	return Config{
		Hasher: fn,
	}
}

// New adds a parser that creates a new named template within the Config.
func New(name string) Config {
	return Config{
		Parsers: []func(tpl *template.Template) (*template.Template, error){
			func(tpl *template.Template) (*template.Template, error) {
				return tpl.New(name), nil
			},
		},
	}
}

// Lookup adds a parser equivalent to template.Lookup(name).
func Lookup(name string) Config {
	return Config{
		Parsers: []func(tpl *template.Template) (*template.Template, error){
			func(tpl *template.Template) (*template.Template, error) {
				t := tpl.Lookup(name)
				if t == nil {
					return nil, fmt.Errorf("template not found: %s", name)
				}

				return t, nil
			},
		},
	}
}

// Parse adds a parser that parses the provided string using template.Parse.
func Parse(txt string) Config {
	return Config{
		Parsers: []func(tpl *template.Template) (*template.Template, error){
			func(tpl *template.Template) (*template.Template, error) {
				return tpl.Parse(txt)
			},
		},
	}
}

// ParseGlob adds a parser that loads templates matching a glob pattern.
func ParseGlob(pattern string) Config {
	return Config{
		Parsers: []func(tpl *template.Template) (*template.Template, error){
			func(tpl *template.Template) (*template.Template, error) {
				return tpl.ParseGlob(pattern)
			},
		},
	}
}

// ParseFiles adds a parser that loads and parses templates from file paths.
func ParseFiles(filenames ...string) Config {
	return Config{
		Parsers: []func(tpl *template.Template) (*template.Template, error){
			func(tpl *template.Template) (*template.Template, error) {
				return tpl.ParseFiles(filenames...)
			},
		},
	}
}

// ParseFS adds a parser that loads templates from an fs.FS source using patterns.
func ParseFS(sys fs.FS, patterns ...string) Config {
	return Config{
		Parsers: []func(tpl *template.Template) (*template.Template, error){
			func(tpl *template.Template) (*template.Template, error) {
				return tpl.ParseFS(sys, patterns...)
			},
		},
	}
}

// Funcs adds a FuncMap to the template.
func Funcs(fm template.FuncMap) Config {
	return Config{
		Parsers: []func(tpl *template.Template) (*template.Template, error){
			func(tpl *template.Template) (*template.Template, error) {
				return tpl.Funcs(fm), nil
			},
		},
	}
}

// Sqlite sets the Dialect to "Sqlite" and the placeholder to "?".
func Sqlite() Config {
	return Dialect("Sqlite").With(Question())
}

// Postgres sets the Dialect to "Postgres" and the placeholder to "$".
func Postgres() Config {
	return Dialect("Postgres").With(Dollar())
}

// Dialect sets the return value of the Dialect() template function, allowing customization per database engine.
// This does not configure placeholders; use With(Question()) or similar if needed.
func Dialect(name string) Config {
	return Config{
		Dialect: name,
	}
}

// ExpressionSize sets the number of reusable expressions to cache.
// Avoid if your templates are non-deterministic.
func ExpressionSize(size int) Config {
	return Config{
		ExpressionSize: size,
	}
}

// ExpressionExpiration sets how long cached expressions are valid.
// Avoid with non-deterministic templates.
func ExpressionExpiration(expiration time.Duration) Config {
	return Config{
		ExpressionExpiration: expiration,
	}
}

// StaticPlaceholder uses the same placeholder string for all parameters (e.g., "?").
func StaticPlaceholder(p string) Config {
	return Config{
		Placeholder: func(_ int, writer io.Writer) error {
			_, err := writer.Write([]byte(p))

			return err
		},
	}
}

// PositionalPlaceholder formats placeholders using a prefix and 1-based index (e.g., "$1", ":1", "@p1").
func PositionalPlaceholder(p string) Config {
	return Config{
		Placeholder: func(pos int, writer io.Writer) error {
			_, err := writer.Write([]byte(string(p) + strconv.Itoa(pos)))

			return err
		},
	}
}

// Question returns a Config that uses "?" as the SQL placeholder (e.g., for SQLite or MySQL).
func Question() Config {
	return StaticPlaceholder("?")
}

// Dollar returns a Config that uses positional placeholders with "$" (e.g., "$1", "$2").
func Dollar() Config {
	return PositionalPlaceholder("$")
}

// Colon returns a Config that uses positional placeholders with ":" (e.g., ":1", ":2").
func Colon() Config {
	return PositionalPlaceholder(":")
}

// AtP returns a Config that uses positional placeholders with "@p" (e.g., "@p1", "@p2").
func AtP() Config {
	return PositionalPlaceholder("@p")
}

// Info contains metadata collected during statement execution for optional logging.
type Info struct {
	Duration time.Duration
	Template string
	Location string
	SQL      string
	Args     []any
	Err      error
	Cached   bool
}

// Expression holds the rendered SQL, arguments, and row mapper.
type Expression[Dest any] struct {
	SQL    string
	Args   []any
	Mapper structscan.Mapper[Dest]
}

// Raw is a string type that inserts raw SQL into a template without interpolation or escaping.
type Raw string

// Exec returns a Statement that executes a SQL statement without returning rows.
func Exec[Param any](configs ...Config) Statement[Param, sql.Result] {
	return newStmt[Param](func(ctx context.Context, db DB, expr Expression[any]) (sql.Result, error) {
		return db.ExecContext(ctx, expr.SQL, expr.Args...)
	}, configs...)
}

// QueryRow returns a Statement that runs the query and returns a single *sql.Row.
func QueryRow[Param any](configs ...Config) Statement[Param, *sql.Row] {
	return newStmt[Param](func(ctx context.Context, db DB, expr Expression[any]) (*sql.Row, error) {
		return db.QueryRowContext(ctx, expr.SQL, expr.Args...), nil
	}, configs...)
}

// Query returns a Statement that runs the query and returns *sql.Rows.
func Query[Param any](configs ...Config) Statement[Param, *sql.Rows] {
	return newStmt[Param](func(ctx context.Context, db DB, expr Expression[any]) (*sql.Rows, error) {
		return db.QueryContext(ctx, expr.SQL, expr.Args...)
	}, configs...)
}

// First returns a Statement that maps the first row from a query to Dest.
func First[Param any, Dest any](configs ...Config) Statement[Param, Dest] {
	return newStmt[Param](func(ctx context.Context, db DB, expr Expression[Dest]) (Dest, error) {
		return expr.Mapper.Row(db.QueryRowContext(ctx, expr.SQL, expr.Args...))
	}, configs...)
}

// One returns a Statement that expects exactly one row in the result set.
// It returns an structscan.ErrTooManyRows if more than one row is returned.
func One[Param any, Dest any](configs ...Config) Statement[Param, Dest] {
	return newStmt[Param](func(ctx context.Context, db DB, expr Expression[Dest]) (Dest, error) {
		rows, err := db.QueryContext(ctx, expr.SQL, expr.Args...)
		if err != nil {
			return *new(Dest), err
		}

		return expr.Mapper.One(rows)
	}, configs...)
}

// All returns a Statement that maps all rows in the result set to a slice of Dest.
func All[Param any, Dest any](configs ...Config) Statement[Param, []Dest] {
	return newStmt[Param](func(ctx context.Context, db DB, expr Expression[Dest]) ([]Dest, error) {
		rows, err := db.QueryContext(ctx, expr.SQL, expr.Args...)
		if err != nil {
			return nil, err
		}

		return expr.Mapper.All(rows)
	}, configs...)
}

// Statement is a compiled SQL template that runs with parameters and a DB.
type Statement[Param, Result any] interface {
	Exec(ctx context.Context, db DB, param Param) (Result, error)
}

// Custom creates a Statement using the provided function to execute the rendered SQL expression.
// This allows advanced behavior such as custom row scanning or side effects.
func Custom[Param any, Dest any, Result any](exec func(ctx context.Context, db DB, expr Expression[Dest]) (Result, error), configs ...Config) Statement[Param, Result] {
	return newStmt[Param](exec, configs...)
}

func newStmt[Param any, Dest any, Result any](exec func(ctx context.Context, db DB, expr Expression[Dest]) (Result, error), configs ...Config) Statement[Param, Result] {
	_, file, line, _ := runtime.Caller(2)

	var (
		location = file + ":" + strconv.Itoa(line)
		config   = Sqlite().With(configs...)
		schema   = structscan.New[Dest]()

		t = template.New("").Option("missingkey=invalid").Funcs(template.FuncMap{
			"Dialect": func() string { return config.Dialect },
			"Raw":     func(sql string) Raw { return Raw(sql) },
			"Dest": func() structscan.Struct[Dest] {
				return schema
			},

			"DateTime":    valueFunc(time.DateTime),
			"DateOnly":    valueFunc(time.DateOnly),
			"TimeOnly":    valueFunc(time.TimeOnly),
			"RFC3339":     valueFunc(time.RFC3339),
			"RFC3339Nano": valueFunc(time.RFC3339Nano),
			"Layout":      valueFunc(time.Layout),
			"ANSIC":       valueFunc(time.ANSIC),
			"UnixDate":    valueFunc(time.UnixDate),
			"RubyDate":    valueFunc(time.RubyDate),
			"RFC822":      valueFunc(time.RFC822),
			"RFC822Z":     valueFunc(time.RFC822Z),
			"RFC850":      valueFunc(time.RFC850),
			"RFC1123":     valueFunc(time.RFC1123),
			"RFC1123Z":    valueFunc(time.RFC1123Z),
			"Kitchen":     valueFunc(time.Kitchen),
			"Stamp":       valueFunc(time.Stamp),
			"StampMilli":  valueFunc(time.StampMilli),
			"StampMicro":  valueFunc(time.StampMicro),
			"StampNano":   valueFunc(time.StampNano),
			"UTC":         valueFunc(time.UTC),
			"Local":       valueFunc(time.Local), //nolint:gosmopolitan
		})
		err error
	)

	for _, p := range config.Parsers {
		t, err = p(t)
		if err != nil {
			panic(fmt.Errorf("statement at %s: parse template: %w", location, err))
		}
	}

	var zero Param
	if err = templatecheck.CheckText(t, zero); err != nil {
		panic(fmt.Errorf("statement at %s: check template: %w", location, err))
	}

	if err = escapeNode(schema, t, t.Root); err != nil {
		panic(fmt.Errorf("statement at %s: escape template: %w", location, err))
	}

	t, err = t.Clone()
	if err != nil {
		panic(fmt.Errorf("statement at %s: clone template: %w", location, err))
	}

	pool := &sync.Pool{
		New: func() any {
			tc, _ := t.Clone()

			r := &runner[Param, Dest]{
				tpl:       tc,
				sqlWriter: &sqlWriter{},
			}

			r.tpl.Funcs(template.FuncMap{
				ident: func(arg any) (Raw, error) {
					switch a := arg.(type) {
					case Raw:
						r.sqlWriter.data = append(r.sqlWriter.data, []byte(a)...)

						return "", nil
					case structscan.Scanner[Dest]:
						r.scanners = append(r.scanners, a)

						return "", nil
					default:
						r.args = append(r.args, arg)

						return "", config.Placeholder(len(r.args), r.sqlWriter)
					}
				},
			})

			return r
		},
	}

	var cache *expirable.LRU[uint64, Expression[Dest]]

	if config.ExpressionSize > 0 || config.ExpressionExpiration > 0 {
		cache = expirable.NewLRU[uint64, Expression[Dest]](config.ExpressionSize, nil, config.ExpressionExpiration)

		if config.Hasher == nil {
			hasher := datahash.New(xxhash.New, datahash.Options{})

			config.Hasher = hasher.Hash
		}

		_, err = config.Hasher(zero)
		if err != nil {
			panic(fmt.Errorf("statement at %s: hashing param: %w", location, err))
		}
	}

	return &statement[Param, Dest, Result]{
		name:     t.Name(),
		location: location,
		cache:    cache,
		pool:     pool,
		logger:   config.Logger,
		exec:     exec,
		hasher:   config.Hasher,
	}
}

// statement is the internal implementation of Statement.
// It holds compiled templates, a result executor, and optional caching/logging.
type statement[Param any, Dest any, Result any] struct {
	name     string
	location string
	cache    *expirable.LRU[uint64, Expression[Dest]]
	exec     func(ctx context.Context, db DB, expr Expression[Dest]) (Result, error)
	pool     *sync.Pool
	logger   func(ctx context.Context, info Info)
	hasher   func(value any) (uint64, error)
}

func (s *statement[Param, Dest, Result]) Exec(ctx context.Context, db DB, param Param) (result Result, err error) {
	var (
		expr   Expression[Dest]
		hash   uint64
		cached bool
	)

	if s.logger != nil {
		now := time.Now()

		defer func() {
			s.logger(ctx, Info{
				Template: s.name,
				Location: s.location,
				Duration: time.Since(now),
				SQL:      expr.SQL,
				Args:     expr.Args,
				Err:      err,
				Cached:   cached,
			})
		}()
	}

	if s.cache != nil {
		hash, err = s.hasher(param)
		if err != nil {
			return result, fmt.Errorf("statement at %s: hashing param: %w", s.location, err)
		}

		expr, cached = s.cache.Get(hash)
		if cached {
			result, err = s.exec(ctx, db, expr)
			if err != nil {
				return result, fmt.Errorf("statement at %s: cached execution: %w", s.location, err)
			}

			return result, nil
		}
	}

	r := s.pool.Get().(*runner[Param, Dest])

	expr, err = r.expr(param)
	if err != nil {
		return result, fmt.Errorf("statement at %s: expression: %w", s.location, err)
	}

	s.pool.Put(r)

	if s.cache != nil {
		_ = s.cache.Add(hash, expr)
	}

	result, err = s.exec(ctx, db, expr)
	if err != nil {
		return result, fmt.Errorf("statement at %s: execution: %w", s.location, err)
	}

	return result, nil
}

// escapeNode rewrites template nodes to capture SQL fragments, scan targets, and arguments.
// Inspired by https://github.com/mhilton/sqltemplate/blob/main/escape.go.
func escapeNode[Dest any](s structscan.Struct[Dest], t *template.Template, n parse.Node) error {
	switch v := n.(type) {
	case *parse.ActionNode:
		return escapeNode(s, t, v.Pipe)
	case *parse.IfNode:
		return twoErrors(
			escapeNode(s, t, v.List),
			escapeNode(s, t, v.ElseList),
		)
	case *parse.ListNode:
		if v == nil {
			return nil
		}

		for _, n := range v.Nodes {
			if err := escapeNode(s, t, n); err != nil {
				return err
			}
		}
	case *parse.PipeNode:
		if len(v.Decl) > 0 {
			return nil
		}

		if len(v.Cmds) < 1 {
			return nil
		}

		if len(v.Cmds[0].Args) > 0 && v.Cmds[0].Args[0].String() == "Scan" && len(v.Cmds[0].Args) > 0 {
			if str, ok := v.Cmds[0].Args[1].(*parse.StringNode); ok {
				if _, ok := s[str.Text]; !ok {
					return fmt.Errorf("field not found: %s", str.Text)
				}
			}
		}

		cmd := v.Cmds[len(v.Cmds)-1]
		if len(cmd.Args) == 1 && cmd.Args[0].Type() == parse.NodeIdentifier && cmd.Args[0].(*parse.IdentifierNode).Ident == ident {
			return nil
		}

		v.Cmds = append(v.Cmds, &parse.CommandNode{
			NodeType: parse.NodeCommand,
			Args:     []parse.Node{parse.NewIdentifier(ident).SetTree(t.Tree).SetPos(cmd.Pos)},
		})
	case *parse.RangeNode:
		return twoErrors(
			escapeNode(s, t, v.List),
			escapeNode(s, t, v.ElseList),
		)
	case *parse.WithNode:
		return twoErrors(
			escapeNode(s, t, v.List),
			escapeNode(s, t, v.ElseList),
		)
	case *parse.TemplateNode:
		tpl := t.Lookup(v.Name)
		if tpl == nil {
			return fmt.Errorf("template %s not found", v.Name)
		}

		return escapeNode(s, tpl, tpl.Root)
	}

	return nil
}

const ident = "__sqlt__"

// runner holds the state for a single template execution.
type runner[Param any, Dest any] struct {
	tpl       *template.Template
	sqlWriter *sqlWriter
	args      []any
	scanners  []structscan.Scanner[Dest]
}

// expr renders the template and collects SQL, args, and structscan mappers.
func (r *runner[Param, Dest]) expr(param Param) (Expression[Dest], error) {
	if err := r.tpl.Execute(r.sqlWriter, param); err != nil {
		return Expression[Dest]{}, err
	}

	expr := Expression[Dest]{
		SQL:    r.sqlWriter.String(),
		Args:   slices.Clone(r.args),
		Mapper: structscan.Map(r.scanners...),
	}

	r.sqlWriter.Reset()
	r.args = r.args[:0]
	r.scanners = r.scanners[:0]

	return expr, nil
}

// sqlWriter accumulates SQL output with normalization (collapses whitespace).
type sqlWriter struct {
	data []byte
}

func (w *sqlWriter) Write(data []byte) (int, error) {
	for _, b := range data {
		switch b {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0:
			if len(w.data) > 0 && w.data[len(w.data)-1] != ' ' {
				w.data = append(w.data, ' ')
			}
		default:
			w.data = append(w.data, b)
		}
	}

	return len(data), nil
}

func (w *sqlWriter) Reset() {
	w.data = w.data[:0]
}

func (w *sqlWriter) String() string {
	n := len(w.data)

	if n == 0 {
		return ""
	} else if w.data[n-1] == ' ' {
		n--
	}

	return string(w.data[:n])
}

// valueFunc wraps a value as a no-arg template function returning it.
func valueFunc[T any](t T) func() T {
	return func() T {
		return t
	}
}

func twoErrors(err1, err2 error) error {
	if err1 == nil {
		return err2
	}

	if err2 == nil {
		return err1
	}

	return errors.Join(err1, err2)
}
