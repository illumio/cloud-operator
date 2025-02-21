# `fakeserver`
Illumio's `cloud-operator` streams data out to a server that you control.
The `fakeserver` provides a convenient way to test out the cloud-operator
without having a server to collect all those streams.

# Running the `fakeserver`
From the repo root:

    go run ./fakeserver

# Pointing `cloud-operator` to `fakeserver`

`fakeserver` accepts a fake set of credentials. You can find the constants in
the code:

    # fakeserver respects these:
    #    DefaultClientID     = "client_id_1"
    #    DefaultClientSecret = "client_secret_1"
    onboardingSecret:
      clientId: "client_id_1"
      clientSecret: "client_secret_1"

Next, you must put the IP address of where `fakeserver` can be reached *from the
context of the cluster*. Assuming that you're running `fakeserver` on the same
host you're running the `k8s` cluster that `cloud-operator` is installed into,
your first guess may be `localhost`. But that's not quite correct - as
`cloud-operator` actually runs inside of a pod. Thus, you must use
`host.docker.internal` to get to the host machine.

And as for the port, well `fakeserver` serves on `50053`, by default

    # This is where my k8s cluster can find my locally running services.
    # '192.168.65.254' is some sort of magic IP addr for k8s...
    env:
      tlsSkipVerify: true
      onboardingEndpoint: "https://host.docker.internal:50053/api/v1/k8s_cluster/onboard"
      tokenEndpoint: "https://host.docker.internal:50053/api/v1/k8s_cluster/authenticate"

## Putting it all together

I've gone ahead and created a file `./cloud-operator.fakeserver.yaml` that
contains all of these data. You can use it directly to install an instance of
`cloud-operator` and run it against `fakeserver`.

Here's the command I used to install the helm chart. The `--values` flag is the
interesting part. By using these values, you will configure the `cloud-operator`
to do the oauth onboarding handshake against `fakeserver` and then send all
flows to it:

    helm install illumio --namespace illumio-cloud --values ./fakeserver/cloud-operator.fakeserver.yaml ./cloud-operator/cloud-operator-1.0.0.tgz
