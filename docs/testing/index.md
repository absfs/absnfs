---
layout: default
title: Testing
---

# Testing and Code Quality

This section provides information about the testing approach and code quality of ABSNFS. Transparency about testing and code quality is important for users to make informed decisions about adopting the library.

## Overview

ABSNFS has a comprehensive test suite that covers core functionality, edge cases, and error scenarios. The test suite is designed to ensure correctness, reliability, and compatibility with the NFS protocol.

## Documentation

- [Test Strategy](./strategy.md): The overall testing strategy and goals
- [Test Types](./types.md): Different types of tests used (unit, integration, etc.)
- [Running Tests](./running-tests.md): How to run the test suite
- [Integration Testing Safety](./integration-safety.md): Safety procedures for privileged test operations

## Running the Test Suite

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run with race detector
go test -race ./...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

## Contributing

When contributing tests:

1. Follow existing test patterns
2. Include both positive and negative test cases
3. Use table-driven tests where appropriate
4. Document test cases clearly
