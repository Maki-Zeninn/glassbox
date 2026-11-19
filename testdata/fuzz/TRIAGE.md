# Fuzz Crash and Timeout Triage Guide

This document provides instructions for triaging fuzz test failures, crashes, and timeouts.

## Crash Triage

### When a Crash is Detected

1. **Reproduce the Crash**
   ```bash
   # Run the specific fuzz target with the crashing input
   go test -fuzz=FuzzTargetName -fuzztime=30s ./path/to/package
   ```

2. **Minimize the Input**
   ```bash
   # Use the fuzzer's built-in minimization
   go test -fuzz=FuzzTargetName -fuzztime=30s ./path/to/package
   
   # The minimized input will be in testdata/fuzz/FuzzTargetName/
   ```

3. **Extract the Crash Input**
   ```bash
   # The fuzzer saves crashing inputs to the corpus directory
   ls testdata/fuzz/FuzzTargetName/
   ```

4. **Create a Reproducible Test Case**
   ```bash
   # Add the minimized input as a seed
   cp <crash_input> testdata/fuzz/corpus/<target>/<descriptive_name>
   
   # Add a unit test to prevent regression
   # See: testdata/fuzz/REGRESSION_TEST_TEMPLATE.md
   ```

5. **Analyze the Root Cause**
   - Review the stack trace
   - Check for buffer overflows, null pointer dereferences, or integer overflows
   - Verify input validation is sufficient
   - Check for resource exhaustion (memory, CPU)

6. **Fix the Issue**
   - Add proper input validation
   - Add bounds checking
   - Add error handling for edge cases
   - Consider adding fuzzing-specific assertions

7. **Verify the Fix**
   ```bash
   # Re-run the fuzzer with the crash input
   go test -fuzz=FuzzTargetName -fuzztime=30s ./path/to/package
   
   # Run the full fuzz suite
   make test-fuzz
   ```

### Crash Classification

- **Critical**: Panic, segmentation fault, or memory safety violation
- **High**: Unhandled error that should be caught
- **Medium**: Timeout or resource exhaustion
- **Low**: Expected error for malformed input (not a crash)

## Timeout Triage

### When a Timeout Occurs

1. **Identify the Slow Path**
   ```bash
   # Run with verbose output to see where it hangs
   go test -v -fuzz=FuzzTargetName -fuzztime=10s ./path/to/package
   ```

2. **Profile the Fuzz Target**
   ```bash
   # Add CPU profiling to the fuzz target
   go test -cpuprofile=cpu.prof -fuzz=FuzzTargetName -fuzztime=10s ./path/to/package
   go tool pprof cpu.prof
   ```

3. **Add Execution Bounds**
   ```go
   // In the fuzz function, add timeout checks
   func FuzzTarget(f *testing.F) {
       f.Fuzz(func(t *testing.T, data []byte) {
           // Add early exit for large inputs
           if len(data) > 1_000_000 {
               return
           }
           
           // Add timeout check if needed
           // ...
       })
   }
   ```

4. **Optimize the Target**
   - Reduce algorithmic complexity
   - Add early validation to reject obviously invalid inputs
   - Limit recursion depth
   - Add size limits on parsed structures

5. **Adjust Fuzz Timeouts**
   ```bash
   # Increase timeout for complex targets
   go test -fuzz=FuzzTargetName -fuzztime=60s -timeout=120s ./path/to/package
   ```

### Timeout Classification

- **Critical**: Infinite loop or unbounded recursion
- **High**: Exponential time complexity
- **Medium**: Slow but bounded execution
- **Low**: Expected slowness for large inputs

## Regression Prevention

### Adding Regression Tests

When a crash or timeout is fixed, add a regression test:

```go
func TestRegression_Crash_IssueXXX(t *testing.T) {
    // Load the crash input from corpus
    crashInput := loadCrashInput("testdata/fuzz/corpus/target/crash_input.bin")
    
    // This should not panic
    result, err := ParseFunction(crashInput)
    
    // Expect an error for malformed input, but no panic
    if err == nil {
        t.Log("Input was unexpectedly valid")
    }
    _ = result
}
```

### Updating the Corpus

1. **Add Crash Inputs to Corpus**
   ```bash
   cp <crash_input> testdata/fuzz/corpus/<target>/regression_<issue>.bin
   ```

2. **Document the Fix**
   - Add a comment in the corpus README
   - Reference the issue number
   - Describe the vulnerability

3. **Version the Corpus**
   - Tag the corpus directory with the fix version
   - Maintain a changelog in the corpus README

## CI Integration

### Fuzz Failures in CI

When CI reports a fuzz failure:

1. **Download the Artifacts**
   - CI should save crash inputs as artifacts
   - Download the failing corpus

2. **Reproduce Locally**
   ```bash
   # Use the exact corpus from CI
   cp -r <ci_corpus> testdata/fuzz/FuzzTargetName/
   go test -fuzz=FuzzTargetName -fuzztime=30s ./path/to/package
   ```

3. **Triage Following the Steps Above**
   - Follow crash or timeout triage as appropriate
   - Document the findings

4. **Update CI Configuration**
   - If the failure is a false positive, add an exclusion
   - If it's a real bug, fix it and update the corpus

## Reporting

### Bug Report Template

```markdown
## Fuzz Failure Report

**Target**: FuzzTargetName
**Package**: path/to/package
**Severity**: Critical/High/Medium/Low

### Reproduction
```bash
go test -fuzz=FuzzTargetName -fuzztime=30s ./path/to/package
```

### Crash Input
Attached: crash_input.bin

### Stack Trace
```
[stack trace here]
```

### Root Cause
[description of the vulnerability]

### Fix
[description of the fix]

### Regression Test
[link to regression test]
```

## Best Practices

1. **Never Ignore Crashes**: All crashes must be investigated and fixed
2. **Minimize Before Analysis**: Always minimize crash inputs first
3. **Document Everything**: Keep records of all triage decisions
4. **Update Corpus**: Add all crash inputs to the corpus
5. **Add Regression Tests**: Ensure bugs don't reoccur
6. **Review Regularly**: Periodically review timeout thresholds
7. **Share Knowledge**: Document common patterns and solutions

## Resources

- [Go Fuzzing Documentation](https://go.dev/doc/fuzz/)
- [OSS-Fuzz Triage Guide](https://google.github.io/oss-fuzz/getting-started/bug-handling/)
- [Fuzzing Book](https://www.fuzzingbook.org/)
