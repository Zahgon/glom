# glom

*Restructuring data, the Go way* — a Go port of [glom](https://github.com/mahmoud/glom),
the Python library for declarative access to nested data and its restructuring.

Real applications have real data, and real data nests. glom gives you:

* path-based access to nested data (`"a.b.1"`, `Path`, `T`, star paths),
* declarative transformation with dict, list and tuple specs,
* readable errors that show where in the target and the spec processing failed,
* matching and validation (`Match`, `Check`, `Switch`), mutation (`Assign`,
  `Delete`), streaming (`Iter`) and reduction (`Sum`, `Fold`, `Flatten`,
  `Merge`, `Group`).

This module reproduces the original's behaviour exactly, including what glom
inherits from CPython 3.12: targets and specs are Python values, results print
as Python's `repr()` prints them, and errors carry CPython's classes and
messages inside glom's "Target-spec trace".

## Usage

```go
import glom "github.com/mahmoud/glom"

target, _ := glom.NewDict(
	[]glom.Value{glom.String("a")},
	[]glom.Value{glom.List(glom.Int(1), glom.Int(2))},
)
got, err := glom.Glom(target, glom.String("a.1"), glom.Options{})
if err != nil {
	var e *glom.Error
	if errors.As(err, &e) {
		fmt.Println(e.Class, e.Message)
	}
}
s, _ := glom.Repr(got) // "2"
```

Values are built with `None`, `Bool`, `Int`, `BigInt`, `Float`, `String`,
`Bytes`, `List`, `Tuple`, `NewDict`, `NewSet`, `NewFrozenSet`, `NewObject` and
`Slice`; named callables and classes (`len`, `int`, `KeyError`,
`operator.add`, `str.upper`, glom's error classes, ...) come from `Builtin`.
Every spec type of the original has a constructor here: `Path`, `T`/`S`/`A`,
`Spec`, `Val`, `Vars`, `Ref`, `Auto`, `Fill`, `Pipe`, `Call`, `Invoke`,
`Coalesce`, `Match`, `Regex`, `And`, `Or`, `Not`, `M`, `Optional`, `Required`,
`Switch`, `Check`, `Assign`, `Delete`, `Iter`, `Fold`, `Sum`, `Flatten`,
`Merge`, `Group` and its aggregators.

The module has no dependencies outside the Go standard library. Regular
expressions follow CPython 3.12's `re` module (`internal/pyre`), and character
properties Unicode 15.0 as CPython 3.12 sees them (`internal/pyuni`).

## Documentation

The `docs/` directory documents glom's behaviour and specifier types; the Go
API mirrors it one to one (see the package documentation).

## License

BSD 3-Clause, see [LICENSE](LICENSE). `internal/pyre`, `internal/pyuni` and
`sort.go` are ports of CPython code under the PSF License.
