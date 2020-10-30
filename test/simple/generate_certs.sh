#!/bin/bash
rm -rf ./certs

mkdir -p ./certs/local
mkdir -p ./certs/remote

connector generate-certs --ca ./certs

# Generate the leaf certs for the k8s connector
connector generate-certs \
          --leaf \
          --ip-address 127.0.0.1 \
          --dns-name "${K8S_PUBLIC_ADDRESS}" \
          --dns-name "${K8S_PUBLIC_ADDRESS_API}" \
          --dns-name "${K8S_PUBLIC_ADDRESS_GRPC}" \
          --dns-name ":9090" \
          --dns-name ":9091" \
          --dns-name "connector" \
          --dns-name "connector:9090" \
          --dns-name "connector:9091" \
					--dns-name "*.container.shipyard.run:9090" \
					--dns-name "*.container.shipyard.run:9091" \
					--dns-name "*.container.shipyard.run:9092" \
					--dns-name "*.container.shipyard.run:9093" \
          --root-ca ./certs/root.cert \
          --root-key ./certs/root.key \
          ./certs/local

connector generate-certs \
          --leaf \
          --ip-address 127.0.0.1 \
          --dns-name "${K8S_PUBLIC_ADDRESS}" \
          --dns-name "${K8S_PUBLIC_ADDRESS_API}" \
          --dns-name "${K8S_PUBLIC_ADDRESS_GRPC}" \
          --dns-name ":9090" \
          --dns-name ":9091" \
          --dns-name "connector" \
          --dns-name "connector:9090" \
          --dns-name "connector:9091" \
					--dns-name "*.container.shipyard.run:9090" \
					--dns-name "*.container.shipyard.run:9091" \
					--dns-name "*.container.shipyard.run:9092" \
					--dns-name "*.container.shipyard.run:9093" \
          --root-ca ./certs/root.cert \
          --root-key ./certs/root.key \
          ./certs/remote
