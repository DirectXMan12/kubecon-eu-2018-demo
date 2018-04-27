FROM scratch

COPY loadbalancer /loadbalancer

ENTRYPOINT ["/loadbalancer"]
