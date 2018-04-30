Horizontal Pod Autoscaler Reloaded: Scale on Custom Metrics
===========================================================

These are the demo files for the KubeCon 2018 presentation by
@directxman12 (Solly Ross) and @MaciekPytel (Maciek Pytel).

Layout
------

* The [loadbalancer](loadbalancer) directory contains files for building
  the fake load balancer application used to demonstrate `Object` metrics.

* The [adapter-config](adapter-config) directory contains the files used
  to run the Prometheus adapter.

* The [prometheus-config](prometheus-config) directory contains the files
  used to deploy Prometheus.

* The [cpu-consumer-config](cpu-consumer-config) directory contains the
  files used to deploy the sample cpu-consuming application that we
  autoscale.

Instructions
------------

1. Ensure your cluster is fully set up.  Make sure you have metrics-server
   running (or some other provider of metrics.k8s.io, if you're from the
   future).  Make sure that your API aggregation certificates, etc are set
   up.  If you use an installer, this is probably done for you.

2. Make sure Prometheus is installed and set up to collect metrics from
   pods.  You can use the Prometheus Operator if you're not familiar with
   setting up Prometheus from scratch.

3. Build and install the right version of the Prometheus adapter.  You'll
   need a version with the "advanced configuration" feature. It currently
   lives at
   [feature/advanced-config](https://github.com/DirectXMan12/k8s-prometheus-adapter/tree/feature/advanced-config),
   but it'll probably be in master at some point in the future. There's an
   image version of it available at
   `docker.io/directxman12/k8s-prometheus-adapter:advanced-config`.

   An appropriate configuration for use in this demo (in ConfigMap form)
   it can be found in the file
   [adapter-config/adapter-config.yaml](adapter-config/configmap.yaml).
   Use it and the Deployment and support files in the same directory to
   deploy the adapter.

4. Build and launch the [fake loadbalancer](loadbalancer) application, and
   make sure Prometheus collects its metrics (which can be found at
   `/_metrics`). They end in `_latency_seconds`.

5. Connect to the fake loadbalancer.  It expects the HTTP host header to
   be of the form `<svc>.<namespace>.balance.local`.  It will proxy
   anything on port 80 to port 80 on the service, except the path
   `/_metrics`, which contain its own metrics.
