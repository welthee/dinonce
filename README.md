![dinonce_gopher](gopher.png)
# dinonce - a distributed nonce tracker

[![main branch](https://github.com/welthee/dinonce/actions/workflows/main.yml/badge.svg)](https://github.com/welthee/dinonce/actions/workflows/main.yml)

For most blockchain clients it is essential to keep track of transaction nonces that protect against duplicate 
transactions and replay attacks.

You can read more about nonces [here](https://medium.com/swlh/ethereum-series-understanding-nonce-3858194b39bf).

Normally, a non-partitioned client application, like your mobile wallet app or MetaMask will easily do this for you, 
but tracking nonces gets trickier once your application needs to split-brain and run in a distributed fashion.
Check MetaMask's nonce-tracker implementation [here](https://github.com/MetaMask/nonce-tracker).

dinonce is a nonce ticketing service which we use at [welthee](https://welthee.com) to process transactions(tx) with 
multiple tx executors, making sure that we avoid:

* double spending
* filling a network's tx pool by having gaps in our nonce sequences

## How it works? In a nutshell.
*dinonce* is designed to support ticketing for multiple nonce sequences in parallel.

An identity on a given blockchain should have it's own sequence of nonces.
*dinonce* defines such a sequence as a `lineage`.

For each lineage a *dinonce* client can get a leased nonce *ticket* for a transcation, and should specify a ticket 
*externalId* which should be unique for a transaction for a given lineage. In other words, it should uniquely identify 
a natural transaction in the calling system.

If the operation succeeds, *dinonce* will reserve hold a lease for the tx's newly associated nonce.

Since most tx executors (*dinonce*'s clients) operate with an *at-least-once semantics*, it is possible that the tx 
will:
* complete (a.k.a will be mined on the blockchain)
* will fail for non-transient reasons

If the tx completes successfully, then the client is expected to *close the ticket*, marking it unlease-able forever.
If the tx fails, the client is expected to notify *dinonce* to *release* the ticket. In this case, it should be assigned
to the next lease request, and be re-used as soon as possible, to avoid filling node tx pools on the blockchain network.

## Client Integrations
dinonce is built using a contract first approach with OpenAPI 3.0.
The API definition can be found [here](./api/api.yaml).

You can generate a client library for the language of your choice using the 
[openapi-generator](https://github.com/OpenAPITools/openapi-generator).

## Deployment
dinonce is packaged as a Docker container and pushed automatically to 
[Docker Hub](https://hub.docker.com/repository/docker/welthee/dinonce).
If you're interested, check out the Dockerfile that generates the image [here](./Dockerfile).

Since we are fans of both Kubernetes and Terraform we have created a Helm Chart to easy deployment to Kubernetes.
*The user is responsible to create the ConfigMap `dinonce config`*, which should contain a single `config.yaml` data
entry and have the structure similar to [this example](./.config/config.yaml).

We like to thing about the services we run as self-contained Terraform projects with clear external dependencies, 
so we've created a handy Terraform module directory to help with deployments.

Currently there is just one module that you can use, called `helm-aws-rds-psql`, which will create a managed 
AWS RDS Aurora PostgreSQL database, create a namespace in kubernetes and deploy the aforementioned Helm Chart to your 
cluster.

See a usage [example here](./deployments/terraform/examples/helm-aws-rds-psql).

## Backends
*dinonce* is designed to support multiple storage backends as long as they respect the above described semantics.

The initial backend we are launching is PostgreSQL.
If you'd like to implement a new backend, feel free to do so and open a pull request.
