#!/bin/bash

set -eu

IMAGE_NAME=tcplb
IMAGE_TAG=dev
BUILDER_ROOT=$(pwd)/$(dirname "$0")
BUILD_CONTEXT=$BUILDER_ROOT/..
DOCKERFILE=$BUILDER_ROOT/Dockerfile
DIST_PATH=$BUILDER_ROOT/../dist
TCPLB_BINARY=tcplb

docker build -t $IMAGE_NAME:$IMAGE_TAG -f "$DOCKERFILE" "$BUILD_CONTEXT"

CID=$(docker container create "$IMAGE_NAME":"$IMAGE_TAG")
function cleanup() {
  docker container rm "$CID"
}
trap cleanup EXIT

# TODO regard the container image as a deployable artefact, add container structure tests
# ref: https://github.com/GoogleContainerTools/container-structure-test

mkdir -p "$DIST_PATH"

docker container cp "$CID":/"$TCPLB_BINARY" "$DIST_PATH"/"$TCPLB_BINARY"