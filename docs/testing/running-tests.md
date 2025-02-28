---
layout: default
title: Running Tests
---

# Running Tests

This page provides detailed instructions for running the ABSNFS test suite. Following these instructions will help you verify that ABSNFS is working correctly in your environment and assist with development and debugging.

## Prerequisites

Before running the tests, ensure you have:

1. **Go 1.21 or later** installed
2. All **dependencies** installed
3. Sufficient **permissions** to run tests (some tests may require elevated privileges)

## Basic Test Commands

### Running All Tests

To run the entire test suite:

```bash
go test -v ./...
```

The `-v` flag enables verbose output, showing each test as it runs and its result.

### Running Specific Tests

To run tests in a specific file:

```bash
go test -v ./path/to/file_test.go
```

To run a specific test function:

```bash
go test -v -run TestFunctionName
```

For example, to run only the `TestLookup` function:

```bash
go test -v -run TestLookup
```

You can use regular expressions with the `-run` flag:

```bash
go test -v -run "TestFile.*"  # Runs all tests starting with "TestFile"
```

### Running Tests with Short Mode

Some tests may take a long time to run. You can use the `-short` flag to skip these:

```bash
go test -v -short ./...
```

Tests that support short mode typically check the `testing.Short()` function and skip time-consuming parts when it returns `true`.

## Test Coverage

### Generating Coverage Reports

To run tests with coverage analysis:

```bash
go test -coverprofile=coverage.out ./...
```

This generates a `coverage.out` file containing the coverage data.

### Viewing Coverage Reports

To view the coverage data in your terminal:

```bash
go tool cover -func=coverage.out
```

This shows the coverage percentage for each function.

For a more visual representation, generate an HTML report:

```bash
go tool cover -html=coverage.out -o coverage.html
```

Open `coverage.html` in your browser to see a color-coded view of your code coverage.

### Coverage with Specific Packages

To generate coverage for specific packages:

```bash
go test -coverprofile=coverage.out github.com/absfs/absnfs
```

### Test Coverage Goals

ABSNFS aims for the following coverage targets:

- **Critical path operations**: >90% coverage
- **Error handling paths**: >85% coverage
- **Overall package**: >80% coverage

## Advanced Testing Options

### Race Detection

To check for race conditions:

```bash
go test -race ./...
```

This enables Go's race detector, which can identify concurrent access issues.

### Timeout Control

Set a maximum time for tests to run:

```bash
go test -timeout 5m ./...  # 5-minute timeout
```

### Parallel Tests

Control the number of tests running in parallel:

```bash
go test -parallel 4 ./...  # Run up to 4 tests in parallel
```

### Benchmarking

To run benchmark tests:

```bash
go test -bench=. ./...
```

For more detailed benchmarking:

```bash
go test -bench=. -benchmem -benchtime=5s ./...
```

The options:
- `-benchmem`: Show memory allocation statistics
- `-benchtime=5s`: Run each benchmark for 5 seconds

To run specific benchmarks:

```bash
go test -bench=BenchmarkRead ./...
```

### CPU and Memory Profiling

Generate CPU profiles:

```bash
go test -cpuprofile=cpu.prof -bench=. ./...
```

Generate memory profiles:

```bash
go test -memprofile=mem.prof -bench=. ./...
```

Analyze profiles with:

```bash
go tool pprof cpu.prof
go tool pprof mem.prof
```

## CI/CD Integration

### GitHub Actions Example

Here's an example GitHub Actions workflow for testing ABSNFS:

```yaml
name: Test

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go-version: [ '1.21.x', '1.22.x' ]

    steps:
    - uses: actions/checkout@v3
    
    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: ${{ matrix.go-version }}
    
    - name: Install dependencies
      run: go mod download
    
    - name: Test
      run: go test -v -race -coverprofile=coverage.out ./...
    
    - name: Upload coverage
      uses: codecov/codecov-action@v3
      with:
        file: ./coverage.out
```

## Troubleshooting Tests

### Common Issues

#### Permission Errors

Some tests may require elevated privileges to bind to privileged ports (like 2049). Run with:

```bash
sudo go test -v ./...
```

Alternatively, configure tests to use non-privileged ports:

```bash
GO_TEST_NFS_PORT=8049 go test -v ./...
```

#### Test Hangs

If tests hang, use a timeout:

```bash
go test -v -timeout 2m ./...
```

You can also run with `-v` to see which test is hanging.

#### Network Issues

If tests fail due to network issues, check:

1. Firewall settings
2. Port availability
3. Local network configuration

#### Memory-Related Failures

For tests that fail due to memory limits:

```bash
GOGC=100 go test -v ./...  # Adjust garbage collection
```

### Debugging Test Failures

Use the testing package's verbose logging:

```go
t.Logf("Detailed debug info: %+v", someValue)
```

Run with `-v` to see these logs:

```bash
go test -v -run TestFailing ./...
```

For more detailed state information, add debug prints to the code temporarily.

## Testing Environment Variables

ABSNFS tests respect several environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `GO_TEST_NFS_PORT` | Port to use for NFS tests | 2049 |
| `GO_TEST_TIMEOUT` | Default timeout for operations | "30s" |
| `GO_TEST_VERBOSE` | Enable verbose logging | false |

Example usage:

```bash
GO_TEST_NFS_PORT=8049 GO_TEST_TIMEOUT=60s GO_TEST_VERBOSE=1 go test -v ./...
```

## Specialized Test Categories

### Integration Tests

Integration tests verify the interaction between components. Run with:

```bash
go test -tags=integration -v ./...
```

### External Tests

Tests that require external resources (like real NFS mounts) can be run with:

```bash
go test -tags=external -v ./...
```

Note that these tests may need special setup and are typically not run in CI.

### Mock Tests

Some tests use mocks to simulate dependencies:

```bash
go test -v ./mock/...
```

## Test Data Management

Tests create temporary data in these locations:

1. System temp directory (via `os.TempDir()`)
2. In-memory when using `memfs`

Temporary files are typically cleaned up after tests, but you can force cleanup:

```bash
go test -v -cleanup=all ./...
```

## Contributing Tests

When contributing to ABSNFS, please follow these testing guidelines:

1. **Write Tests First**: Follow test-driven development principles
2. **Test Edge Cases**: Include tests for error conditions and edge cases
3. **Benchmark Changes**: Include benchmarks for performance-sensitive code
4. **Clean Up Resources**: Ensure tests clean up after themselves
5. **Avoid External Dependencies**: Tests should run without external services
6. **Use Test Helpers**: Leverage test helper functions for common operations
7. **Document Assumptions**: Include comments explaining test assumptions

## Conclusion

A comprehensive test suite is essential for maintaining ABSNFS's reliability and performance. By following these instructions, you can effectively run tests, identify issues, and contribute to the project.

For more information about ABSNFS testing, see:
- [Test Strategy](./strategy.md)
- [Test Coverage](./coverage.md)
- [Test Types](./types.md)