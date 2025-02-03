#!/bin/bash

# Run Go tests and send it to the background
go test -v ./fakeserver/.. &

# Run Helm upgrade/install command
helm upgrade --install illumio cloud-operator-*.tgz \
  --namespace illumio-cloud \
  --create-namespace \
  --set image.repository=ghcr.io/${{ github.repository }} \
  --set image.tag=${{ github.ref_name }} \
  --set image.pullPolicy=Always \
  --set onboardingSecret.clientId=$ONBOARDING_CLIENT_ID \
  --set onboardingSecret.clientSecret=$ONBOARDING_CLIENT_SECRET \
  --set env.onboardingEndpoint=$ONBOARDING_ENDPOINT \
  --set env.tokenEndpoint=$TOKEN_ENDPOINT \
  --set env.tlsSkipVerify=$TLS_SKIP_VERIFY \
  --set falco.enabled=false

# Wait for the background process (go test) to finish
wait
