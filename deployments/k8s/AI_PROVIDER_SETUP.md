# AI Provider Setup For Local Kubernetes

Use this when you want Stormrunner scripts running in the controller pod to read provider credentials from Kubernetes instead of your local shell.

The local controller deployment supports these optional environment variables:

- `OPENAI_API_KEY`
- `OPENAI_MODEL`
- `XAI_API_KEY`
- `XAI_MODEL`

If you do not create the matching Kubernetes resources, the controller still starts normally, but scripts using those variables will fail at runtime.

## Grok / xAI

Create the secret and default model config:

```bash
kubectl -n stormrunner-local create secret generic stormrunner-xai-secrets \
  --from-literal=XAI_API_KEY=REPLACE_WITH_XAI_API_KEY \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n stormrunner-local create configmap stormrunner-xai-settings \
  --from-literal=XAI_MODEL=grok-4.3 \
  --dry-run=client -o yaml | kubectl apply -f -
```

## OpenAI

Create the secret and default model config:

```bash
kubectl -n stormrunner-local create secret generic stormrunner-openai-secrets \
  --from-literal=OPENAI_API_KEY=REPLACE_WITH_OPENAI_API_KEY \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n stormrunner-local create configmap stormrunner-openai-settings \
  --from-literal=OPENAI_MODEL=REPLACE_WITH_OPENAI_MODEL \
  --dry-run=client -o yaml | kubectl apply -f -
```

## Reload The Controller

After creating or updating either secret or configmap, restart the controller so the pod picks up the new values:

```bash
kubectl -n stormrunner-local rollout restart deployment/stormrunner-controller
kubectl -n stormrunner-local rollout status deployment/stormrunner-controller
```

## Verify The Environment

You can confirm a non-secret model variable is present with:

```bash
kubectl -n stormrunner-local exec deploy/stormrunner-controller -- printenv XAI_MODEL
kubectl -n stormrunner-local exec deploy/stormrunner-controller -- printenv OPENAI_MODEL
```

Do not print the API key values back out of the pod.