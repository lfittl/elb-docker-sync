## elb-docker-sync

This is a really simple Go daemon that allows you to sync the exposed ports of your running Docker
containers to an AWS Elastic Load Balancer that runs as an Application Load Balancer.

You might find this useful if you are not using ECS and/or are running your Docker setup on CoreOS.

## Usage

```
docker run -d -v /var/run/docker.sock:/var/run/docker.sock -m 256m lfittl/elb-docker-sync:latest docker-container-prefix,elb-name
```

The above will start this helper in the background and synchronize all Docker containers whose name
starts with `docker-container-prefix` into the ELB named `elb-name`.

Note that your containers will need to have their ports exposed, e.g. by passing the `-P` flag when starting.
You don't have to keep the same port number over time.

## Rolling Deploys

In case you want to have a smooth rolling deploy of new versions, this helper optionally detects
the version number contained in your container's name (must match the regexp /v\d+/).

When a new version is detected as running, all previous versions will be deregistered from ELB
already. This helps drain the connections and avoids requests going to the old containers about
to be shut down.

## Authors

* [Lukas Fittl](mailto:lukas@fittl.com)

## License

Copyright (c) 2016, Lukas Fittl <lukas@fittl.com><br>
elb-docker-sync is licensed under the 3-clause BSD license, see LICENSE file for details.
