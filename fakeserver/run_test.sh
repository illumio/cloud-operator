#!/bin/bash

# Run Go tests and send it to the background
go test -v ./fakeserver/.. &

cd ..

# Run Helm upgrade/install command
helm upgrade --install illumio ./fakeserver/cloud-operator-1.0.0.tgz \
  --namespace illumio-cloud \
  --create-namespace \
  --set image.repository=ghcr.io/${GITHUB_REPOSITORY} \
  --set image.tag=${GITHUB_REF_NAME} \
  --set image.pullPolicy=Always \
  --set onboardingSecret.clientId=$ONBOARDING_CLIENT_ID \
  --set onboardingSecret.clientSecret=$ONBOARDING_CLIENT_SECRET \
  --set env.onboardingEndpoint=$ONBOARDING_ENDPOINT \
  --set env.tokenEndpoint=$TOKEN_ENDPOINT \
  --set env.tlsSkipVerify=$TLS_SKIP_VERIFY \
  --set falco.enabled=false

# Wait for the background process (go test) to finish
wait
