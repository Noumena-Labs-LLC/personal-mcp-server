# Threat model summary

Primary risks include prompt-injection through local files, accidental broad filesystem access, secret exposure, unsafe command execution, and unintended edits.

Mitigations include configured roots, bearer-token auth, Host/Origin validation, deny rules, bounded reads, argv-only command policy, approval gates, audit logs, and local-only trust for project configs.
