#!/usr/bin/env bash
#
# docker-dev installer script

# Find current platform
uname="$(uname -s)"
case $uname in
    Linux*)     arch=linux;;
    Darwin*)    arch=darwin;;
    *)
      echo "Platform '$uname' not supported"
      exit 1
      ;;
esac

# Find the most recent version from GitHub
json=$(curl -s https://api.github.com/repos/tubbo/docker-dev/releases/latest)
version=$(echo $json | grep "tag" | cut -d : -f 2,3 | tr -d \")

# Download the latest release from GitHub
echo $json \
  | grep "browser_download_url.$arch" \
  | cut -d : -f 2,3 \
  | tr -d \" \
  | curl - \
  | tar zxvf

# Install the binary and run setup
install "./docker-dev-$version-$arch-amd64/docker-dev" /usr/local/bin
docker-dev -install
