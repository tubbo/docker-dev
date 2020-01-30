# Docker Dev

A fork of [puma-dev][] for containerized applications.

## Installation

Run the handy-dandy script:

    curl https://tubbo.github.io/docker-dev/install.sh | bash

Or, install with [Go][]:

    go install github.com/tubbo/docker-dev

Then:

    docker-dev -install

## Usage

Symlink the apps you want to serve with `docker-dev` to the
**~/.docker-dev** path on your machine. You can also use the `link`
subcommand from within your app directory:

    docker-dev link

Before booting your app, make sure your **docker-compose.yml** includes
a port mapping that will expose your application server's HTTP port to
the `$PORT` passed in by `docker-dev`, as well as at least one health
check. You need the `$PORT` to tell `docker-compose` what port
`docker-dev` is expecting the app to be served on, and a `HEALTHCHECK`
in your **Dockerfile** (or in the YAML) to tell `docker-dev` when the
app is ready.

Here's a minimal example to get a container running in `docker-dev`:

```yaml
services:
  web:
    build: .
    healthcheck: # you can also configure this in Dockerfile
      test: ["CMD", "curl", "-f", "http://localhost:3000"]
    ports:
      - '${PORT}:3000'
```

Then, boot your app by requesting https://yourapp.test

## Why?

`puma-dev` makes a lot of sense for Ruby-centric workflows, and is
a well-made piece of software that is fast and generally bug-free. But
using non-Puma web servers with `puma-dev` is a bit of a hassle and
requires a lot of boilerplate configuration in your shell. As many of us
move to Docker-centric, polyglot workflows instead of Ruby-centric
workflows where a Rack application with a UNIX socket is commonplace,
configuring `puma-dev` to work like it did before is cumbersome.

To overcome this, `docker-dev` replaces the functionality of connecting
to Puma over a UNIX socket with connecting to a TCP socket on a
randomized port in the 3001-3999 range. This port is then passed into
the `docker-compose.yml` file by way of an environment variable named
`$PORT`.

[puma-dev]: https://github.com/puma/puma-dev
