# Quality docs summary

Recommended local validation:

```bash
go mod tidy
just ci
just integration-test
just smoke-test
just coverage-profile
```

Integration and smoke tests are native-only and use temporary configs, roots, tokens, audit logs, trust stores, and test-owned subprocesses.
