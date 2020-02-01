---
layout: default
---

# Docker Dev

A fork of [puma-dev][] for containerized applications.

## Installation

Run the handy-dandy script:

    curl https://tubbo.github.io/docker-dev/install.sh | bash

Or, install with [Go][]:

    go install github.com/tubbo/docker-dev

Then:

    docker-dev -install

You can also take a look at the [README][] for information on how to
build from source.

## Usage

Symlink the apps you want to serve with `docker-dev` to the
**~/.docker-dev** path on your machine. You can also use the `link`
subcommand from within your app directory:

    docker-dev link

Before booting your app, make sure your **docker-compose.yml** includes
a port mapping that will expose your application server's HTTP port to
the `$PORT` passed in by `docker-dev`. You need the `$PORT` to tell
`docker-compose` what port `docker-dev` is expecting the app to be served
on, since it's cumbersome for `docker-compose` apps to communicate through
UNIX sockets.

Here's a minimal example to get a container running in `docker-dev`:

```yaml
# in ~/.docker-dev/your-app/docker-compose.yml..
version: '3'
services:
  web:
    build: .
    ports:
      - '${PORT}:3000'
```

Then, boot your app by requesting https://yourapp.test

This will launch `docker-compose` and pass in a randomized `$PORT`. When
your app is ready to serve requests (see below for how to configure
this), the request made to your `.test` domain will complete. Until
then, the request will be enqueued and the browser will wait until the
app is up and running.

### Health Checks

`docker-dev` pays attention to a `HEALTHCHECK` if you have one
configured in your Docker image (or in your compose file). If Docker can
check the health of your container, then `docker-dev` can read that
information and determine what state the application is in, and whether
the project needs a restart. When no health check is configured for the
container, `docker-dev` will signal that the app is ready when the
container is up and running, but a health check allows some deeper
reporting.

An example of when you might want to do this is for a Rails application
that takes a while to boot up. The container may be up, but the web
server is not yet ready to serve requests. However, if you have a `HEALTHCHECK`
configured to run `curl http://localhost:3000/`, Docker (and
`docker-dev`) can be aware of whether the web server running in the
container is ready to serve requests, and enqueue any requests made to
the `.test` domain until that time occurs.

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
[Go]: https://golang.org
[README]: https://github.com/tubbo/docker-dev/blob/master/README.md
