// Package rules compiles and evaluates the sandboxed expressions used in game definitions and bindings.
//
// Expressions are written in the expr language (github.com/expr-lang/expr). They cannot loop forever, assign, or
// touch anything outside the environment they are given. On top of expr's own checks, this package verifies every
// name an expression reads against a Shape describing the environment, so that a typo such as "own.bals.total" is
// reported when the game definition is loaded and not silently evaluated as nil during a match.
package rules

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
)

// Kind is the coarse type of a value in an expression environment.
type Kind int

const (
	Any Kind = iota
	Number
	Bool
	String
	Object
	List
)

// Shape describes a value that expressions may read: its kind and, for objects and lists, what is inside.
type Shape struct {
	Kind   Kind
	Fields map[string]*Shape // For Object.
	Elem   *Shape            // For List.
}

// Convenience constructors.
func Obj(fields map[string]*Shape) *Shape { return &Shape{Kind: Object, Fields: fields} }
func ListOf(elem *Shape) *Shape           { return &Shape{Kind: List, Elem: elem} }

var (
	NumberShape = &Shape{Kind: Number}
	BoolShape   = &Shape{Kind: Bool}
	StringShape = &Shape{Kind: String}
	AnyShape    = &Shape{Kind: Any}
)

// Error is a problem found in an expression, positioned within the expression's own text.
type Error struct {
	Line    int // 1-based line within the expression.
	Column  int // 0-based column within that line.
	Message string
}

func (e *Error) Error() string { return e.Message }

// Program is a compiled expression. It is safe for concurrent use.
type Program struct {
	Source string
	// Roots lists the top-level environment names the expression reads, sorted.
	Roots []string
	// Paths lists every dotted path the expression reads, such as "own.points.balls" or "#.climb", sorted. It is
	// used to order computed values so that each is evaluated after the ones it depends on.
	Paths []string
	prog  *vm.Program
}

// Options controls compilation.
type Options struct {
	// AllowPick permits the pick() function, which returns one of its arguments at random. It is only legal in
	// triggers, where the result is stored and never recomputed.
	AllowPick bool
}

// Functions available to every expression in addition to expr's built-ins.
var functionNames = map[string]bool{"clamp": true, "pick": true}

// Compile parses src, checks every name it reads against roots, and compiles it.
func Compile(src string, roots map[string]*Shape, opts Options) (*Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, &Error{Line: 1, Message: "expression is empty"}
	}
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, convertError(err)
	}

	c := &checker{src: src, roots: roots, opts: opts, rootsUsed: map[string]bool{}, paths: map[string]bool{}}
	c.infer(tree.Node, nil)
	if c.err != nil {
		return nil, c.err
	}

	env := make(map[string]any, len(roots))
	for name, shape := range roots {
		env[name] = zeroFor(shape)
	}
	compileOpts := []expr.Option{
		expr.Env(env),
		expr.Function("clamp", clamp, new(func(float64, float64, float64) float64)),
		expr.Function("pick", pick),
	}
	prog, err := expr.Compile(src, compileOpts...)
	if err != nil {
		return nil, convertError(err)
	}
	return &Program{Source: src, Roots: sortedKeys(c.rootsUsed), Paths: sortedKeys(c.paths), prog: prog}, nil
}

// Run evaluates the program against env. The environment must contain every root the program was compiled with.
func (p *Program) Run(env map[string]any) (any, error) {
	out, err := expr.Run(p.prog, env)
	if err != nil {
		return nil, convertError(err)
	}
	return out, nil
}

// RunNumber evaluates the program and converts the result to a number. Booleans count as 1 and 0, so that a
// condition can be used directly as "one ranking point if true".
func (p *Program) RunNumber(env map[string]any) (float64, error) {
	out, err := p.Run(env)
	if err != nil {
		return 0, err
	}
	n, ok := ToNumber(out)
	if !ok {
		return 0, fmt.Errorf("expression %q gave %v (%T), expected a number", p.Source, out, out)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("expression %q gave %v, which is not a finite number", p.Source, n)
	}
	return n, nil
}

// ToNumber converts an expression result to a float. Booleans become 1 or 0 and nil becomes 0.
func ToNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case nil:
		return 0, true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

// Truthy converts an expression result to a boolean: false, nil, 0 and "" are false.
func Truthy(v any) bool {
	switch b := v.(type) {
	case nil:
		return false
	case bool:
		return b
	case string:
		return b != ""
	}
	n, ok := ToNumber(v)
	return !ok || n != 0
}

// PickFunc chooses among the arguments of pick(). Tests replace it to make triggers deterministic.
var PickFunc = func(options []any) any {
	return options[randIntn(len(options))]
}

func pick(params ...any) (any, error) {
	if len(params) == 0 {
		return nil, errors.New("pick() needs at least one argument")
	}
	return PickFunc(params), nil
}

func clamp(params ...any) (any, error) {
	if len(params) != 3 {
		return nil, errors.New("clamp(value, low, high) takes three arguments")
	}
	var n [3]float64
	for i, p := range params {
		v, ok := ToNumber(p)
		if !ok {
			return nil, fmt.Errorf("clamp() argument %d is not a number", i+1)
		}
		n[i] = v
	}
	return math.Max(n[1], math.Min(n[2], n[0])), nil
}

// zeroFor returns a placeholder value of the right Go type for expr's own type checker.
func zeroFor(shape *Shape) any {
	if shape == nil {
		return map[string]any{}
	}
	switch shape.Kind {
	case Number:
		return float64(0)
	case Bool:
		return false
	case String:
		return ""
	case List:
		return []any{}
	case Object:
		return map[string]any{}
	}
	var unknown any
	return unknown
}

func convertError(err error) error {
	var fe *file.Error
	if errors.As(err, &fe) {
		return &Error{Line: fe.Line, Column: fe.Column, Message: fe.Message}
	}
	return &Error{Line: 1, Message: err.Error()}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checker walks an expression and resolves every identifier and member access against the environment shape.
type checker struct {
	src       string
	roots     map[string]*Shape
	opts      Options
	rootsUsed map[string]bool
	paths     map[string]bool
	err       *Error
}

// scope carries what is visible inside predicates and let bindings.
type scope struct {
	parent  *scope
	pointer *Shape            // Shape of "#" inside a predicate.
	vars    map[string]*Shape // Variables declared with let.
}

func (s *scope) lookupVar(name string) (*Shape, bool) {
	for ; s != nil; s = s.parent {
		if shape, ok := s.vars[name]; ok {
			return shape, true
		}
	}
	return nil, false
}

func (s *scope) lookupPointer() *Shape {
	for ; s != nil; s = s.parent {
		if s.pointer != nil {
			return s.pointer
		}
	}
	return nil
}

func (c *checker) fail(node ast.Node, format string, args ...any) {
	if c.err != nil {
		return
	}
	line, col := position(c.src, node.Location().From)
	c.err = &Error{Line: line, Column: col, Message: fmt.Sprintf(format, args...)}
}

// infer returns the shape of node, or nil when it is not known. It reports the first unknown name it meets.
func (c *checker) infer(node ast.Node, sc *scope) *Shape {
	if c.err != nil || node == nil {
		return nil
	}
	switch n := node.(type) {
	case *ast.IdentifierNode:
		if shape, ok := sc.lookupVar(n.Value); ok {
			return shape
		}
		if shape, ok := c.roots[n.Value]; ok {
			c.rootsUsed[n.Value] = true
			return shape
		}
		if functionNames[n.Value] {
			return nil
		}
		c.fail(node, "unknown name %q%s", n.Value, suggest(n.Value, keysOf(c.roots)))
		return nil

	case *ast.PointerNode:
		if n.Name != "" {
			return nil // #acc and #index inside reduce().
		}
		return sc.lookupPointer()

	case *ast.MemberNode:
		base := c.infer(n.Node, sc)
		switch prop := n.Property.(type) {
		case *ast.StringNode:
			c.paths[describe(node)] = true
			if base == nil || base.Kind == Any {
				return nil
			}
			if base.Kind != Object {
				c.fail(n.Property, "%q is not an object, so it has no property %q", describe(n.Node), prop.Value)
				return nil
			}
			field, ok := base.Fields[prop.Value]
			if !ok {
				c.fail(
					n.Property, "%q has no property %q%s", describe(n.Node), prop.Value,
					suggest(prop.Value, keysOf(base.Fields)),
				)
				return nil
			}
			return field
		default:
			c.infer(n.Property, sc)
			if base != nil && base.Kind == List {
				return base.Elem
			}
			return nil
		}

	case *ast.ChainNode:
		return c.infer(n.Node, sc)

	case *ast.SliceNode:
		base := c.infer(n.Node, sc)
		c.infer(n.From, sc)
		c.infer(n.To, sc)
		return base

	case *ast.UnaryNode:
		c.infer(n.Node, sc)
		return nil

	case *ast.BinaryNode:
		c.infer(n.Left, sc)
		c.infer(n.Right, sc)
		return nil

	case *ast.ConditionalNode:
		c.infer(n.Cond, sc)
		a := c.infer(n.Exp1, sc)
		c.infer(n.Exp2, sc)
		return a

	case *ast.CallNode:
		if ident, ok := n.Callee.(*ast.IdentifierNode); ok && functionNames[ident.Value] {
			if ident.Value == "pick" && !c.opts.AllowPick {
				c.fail(node, "pick() may only be used in a trigger, where its result is stored")
			}
		} else {
			c.infer(n.Callee, sc)
		}
		for _, arg := range n.Arguments {
			c.infer(arg, sc)
		}
		return nil

	case *ast.BuiltinNode:
		var first *Shape
		for i, arg := range n.Arguments {
			if pred, ok := arg.(*ast.PredicateNode); ok {
				inner := &scope{parent: sc, pointer: AnyShape}
				if first != nil && first.Kind == List && first.Elem != nil {
					inner.pointer = first.Elem
				}
				c.infer(pred.Node, inner)
				continue
			}
			shape := c.infer(arg, sc)
			if i == 0 {
				first = shape
			}
		}
		switch n.Name {
		case "filter", "sort", "sortBy", "take", "reverse", "uniq":
			return first
		case "find", "findLast", "first", "last":
			if first != nil && first.Kind == List {
				return first.Elem
			}
		}
		return nil

	case *ast.PredicateNode:
		c.infer(n.Node, sc)
		return nil

	case *ast.VariableDeclaratorNode:
		value := c.infer(n.Value, sc)
		inner := &scope{parent: sc, vars: map[string]*Shape{n.Name: value}}
		return c.infer(n.Expr, inner)

	case *ast.SequenceNode:
		var last *Shape
		for _, child := range n.Nodes {
			last = c.infer(child, sc)
		}
		return last

	case *ast.ArrayNode:
		var elem *Shape
		for _, child := range n.Nodes {
			elem = c.infer(child, sc)
		}
		return ListOf(elem)

	case *ast.MapNode:
		for _, pair := range n.Pairs {
			c.infer(pair, sc)
		}
		return nil

	case *ast.PairNode:
		c.infer(n.Value, sc)
		return nil
	}
	return nil // Literals.
}

func describe(node ast.Node) string {
	switch n := node.(type) {
	case *ast.IdentifierNode:
		return n.Value
	case *ast.PointerNode:
		return "#"
	case *ast.MemberNode:
		if prop, ok := n.Property.(*ast.StringNode); ok {
			return describe(n.Node) + "." + prop.Value
		}
		return describe(n.Node) + "[...]"
	case *ast.ChainNode:
		return describe(n.Node)
	}
	return "this value"
}

func keysOf(m map[string]*Shape) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// suggest returns a "did you mean" hint when a candidate is close to name, or the list of valid names when there
// are only a few.
func suggest(name string, candidates []string) string {
	best, bestDistance := "", 3
	for _, candidate := range candidates {
		if d := editDistance(name, candidate); d < bestDistance {
			best, bestDistance = candidate, d
		}
	}
	if best != "" {
		return fmt.Sprintf(" (did you mean %q?)", best)
	}
	if len(candidates) > 0 && len(candidates) <= 12 {
		return fmt.Sprintf(" (available: %s)", strings.Join(candidates, ", "))
	}
	return ""
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// position converts a byte offset in src to a 1-based line and 0-based column.
func position(src string, offset int) (int, int) {
	if offset > len(src) {
		offset = len(src)
	}
	line, col := 1, 0
	for _, r := range src[:offset] {
		if r == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return line, col
}
