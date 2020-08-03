FROM busybox
WORKDIR /tmp
ARG VERSION=3.11.4
ADD https://github.com/protocolbuffers/protobuf/releases/download/v${VERSION}/protoc-${VERSION}-linux-x86_64.zip .
RUN unzip protoc-${VERSION}-linux-x86_64.zip

FROM golang:1.13.0
COPY --from=0 /tmp/bin/protoc /usr/local/bin/protoc
COPY --from=0 /tmp/include /usr/local/include
RUN chmod +x /usr/local/bin/protoc
RUN wget https://github.com/grpc/grpc-web/releases/download/1.0.7/protoc-gen-grpc-web-1.0.7-linux-x86_64 -O /bin/protoc-gen-grpc-web && chmod +x /bin/protoc-gen-grpc-web
RUN wget https://github.com/WitBookingSysAdmin/protoc-gen-jsonschema/releases/download/v0.9.3/protoc-gen-jsonschema-linux-amd64 -O /bin/protoc-gen-jsonschema && chmod +x /bin/protoc-gen-jsonschema
RUN wget https://github.com/bufbuild/buf/releases/download/v0.8.0/buf-Linux-x86_64 -O /bin/buf && chmod +x /bin/buf