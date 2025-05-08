# Testing the Helm Chart JSON Schema Validation

This guide explains how to test the JSON schema validation for the cloud-operator Helm chart.

## Prerequisites

- Helm v3.15.4 or later (required for JSON schema validation support)
- Access to the cloud-operator Helm chart

## Basic Testing

### 1. Test with Incorrect Format (Should Fail)

The following command attempts to install the chart with an incorrect format for the `falco` parameter:

```bash
helm install --dry-run my-release ./cloud-operator --set falco=enabled
```

Expected output:
```
Error: values don't meet the specifications of the schema(s) in the following chart(s):
cloud-operator:
- falco: must be object
```

### 2. Test with Correct Format (Should Pass)

The following command uses the correct format for the `falco` parameter:

```bash
helm install --dry-run my-release ./cloud-operator --set falco.enabled=true
```

Expected output:
```
# A successful dry-run output without validation errors
```

## Testing with a Values File

### 1. Create a Valid Values File

Create a file named `valid-values.yaml` with the following content:

```yaml
falco:
  enabled: true
  namespace: falco
```

Test it with:
```bash
helm install --dry-run my-release ./cloud-operator -f valid-values.yaml
```

### 2. Create an Invalid Values File

Create a file named `invalid-values.yaml` with the following content:

```yaml
falco: enabled  # Incorrect format
```

Test it with:
```bash
helm install --dry-run my-release ./cloud-operator -f invalid-values.yaml
```

This should produce a validation error.

## Testing Other Schema Validations

The schema validates many other fields. Here are some examples to test:

### Testing Required Fields

```bash
# Missing required field in image section
helm install --dry-run my-release ./cloud-operator --set image.repository=test --set image.tag=latest
```

Expected output: Error about missing `pullPolicy` field

### Testing Enum Values

```bash
# Invalid enum value for pullPolicy
helm install --dry-run my-release ./cloud-operator --set image.pullPolicy=Sometimes
```

Expected output: Error that `Sometimes` is not a valid value for `pullPolicy`

### Testing Type Validations

```bash
# Wrong type for replicaCount (string instead of integer)
helm install --dry-run my-release ./cloud-operator --set replicaCount=one
```

Expected output: Error that `replicaCount` must be an integer

## Troubleshooting

### Common Issues

1. **Schema Not Being Applied**: Ensure your Helm version supports JSON schema validation (v3.5.0+)

2. **Unexpected Validation Errors**: Check the exact path of the property causing the error. The error message should indicate which property failed validation and why.

3. **No Validation Errors When Expected**: Verify that the schema file is correctly located at `cloud-operator/values.schema.json` and that it contains the proper validation rules.

### Verifying Schema Location

The schema file must be located at the root of the Helm chart. Verify with:

```bash
ls -la cloud-operator/values.schema.json
```

## Advanced Testing

For more thorough testing, you can use the `helm lint` command:

```bash
helm lint ./cloud-operator -f your-values.yaml
```

This will check for any issues with your chart, including schema validation errors.