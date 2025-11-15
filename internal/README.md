# Go Core Path Generation - Learning Guide

This directory contains the Go port of the core path generation logic from the Python codebase. The code is heavily annotated to help you learn Go syntax and idioms.

## File Structure

- **types.go**: Core data structures (Point, Layer, Wind, etc.)
- **mandrel.go**: Mandrel profile handling and interpolation
- **pathgen.go**: Path generation algorithms (hoop and helical)
- **gcode.go**: G-code string generation

## Key Go Concepts Explained

### 1. Package Declaration
```go
package internal
```
- Every Go file starts with a package declaration
- Files in the same directory belong to the same package
- `internal` is a special package name that makes it unimportable from outside the module

### 2. Structs vs Python Classes
```go
type Point struct {
    X float64
    Y float64
}
```
- Go uses structs instead of classes
- No inheritance, but you can embed structs
- Fields are explicitly typed (no dynamic typing)

### 3. Methods
```go
func (p Point) Add(p2 Point) Point {
    return Point{X: p.X + p2.X, Y: p.Y + p2.Y}
}
```
- Methods are functions with receivers
- `(p Point)` is the receiver - like `self` in Python
- Go doesn't have operator overloading, so we use methods

### 4. Slices vs Lists
```go
points := make([]Point, 0, 10)  // length 0, capacity 10
points = append(points, newPoint)
```
- Slices are like Python lists but typed
- `make([]Type, length, capacity)` pre-allocates
- `append()` returns a new slice (may reallocate)

### 5. Error Handling
```go
result, err := someFunction()
if err != nil {
    return nil, err
}
```
- Go uses explicit error returns (no exceptions)
- Always check errors
- Functions return `(result, error)` tuples

### 6. Type Assertions
```go
if p, ok := item.(Point); ok {
    // item is a Point
}
```
- Used to check if an interface{} is a specific type
- Returns the value and a boolean indicating success

### 7. Pointers
```go
func (m *Mandrel) Interp(x float64) float64 {
    // *Mandrel is a pointer receiver
}
```
- `*Type` is a pointer to Type
- Pointers avoid copying large structs
- Methods can have value or pointer receivers

### 8. Multiple Return Values
```go
func GenPointsHoop(...) ([]Point, []Point) {
    return fwpath, bwpath
}
```
- Go functions can return multiple values
- More explicit than Python's tuple unpacking

### 9. Switch Statements
```go
switch v := value.(type) {
case int:
    // v is int
case float64:
    // v is float64
}
```
- Type switches are powerful for handling different types
- More explicit than Python's isinstance()

### 10. Defer
```go
file, err := os.Open(path)
defer file.Close()  // Always closes, even if function returns early
```
- `defer` schedules a function call for when the current function returns
- Great for cleanup (like Python's `with` statement)

## Differences from Python

1. **No operator overloading**: Use methods instead of `__add__`
2. **Explicit types**: Everything must be typed
3. **No default parameters**: Use constructor functions
4. **Error handling**: Explicit errors, no exceptions
5. **No list comprehensions**: Use loops and append()
6. **No dynamic attributes**: Use structs with fixed fields
7. **Value vs reference**: Structs are copied by value unless you use pointers

## Next Steps

1. Read through each file in order (types.go → mandrel.go → pathgen.go → gcode.go)
2. Compare with the Python versions in `src/`
3. Try modifying the code to see how Go's type system works
4. Run `go build` to compile and check for errors

## Building

```bash
go build ./internal  # Compile the package
go test ./internal   # Run tests (when you add them)
```

