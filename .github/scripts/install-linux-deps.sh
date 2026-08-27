#!/usr/bin/env bash
# Native development headers required to build the client stack on Linux.
# ebitengine links against X11/OpenGL for the window and ALSA for audio; with
# these absent `go build ./...` fails before vet and tests ever run, which is
# how the whole suite could look healthy while never executing.
set -euo pipefail

sudo apt-get update
sudo apt-get install -y --no-install-recommends \
  gcc \
  pkg-config \
  libgl1-mesa-dev \
  libxrandr-dev \
  libxcursor-dev \
  libxinerama-dev \
  libxi-dev \
  libxxf86vm-dev \
  libasound2-dev
