# Zero-OS Base ![Tests](https://github.com/threefoldtech/zos_base/workflows/Tests%20and%20Coverage/badge.svg) [![Go Report Card](https://goreportcard.com/badge/github.com/threefoldtech/zos)](https://goreportcard.com/report/github.com/threefoldtech/zos)

Zero-OS Base contains shared foundational packages and core libraries common to all Zero-OS variants. It abstracts hardware interfaces, networking primitives, and storage operations used across the Zero-OS family.

## What this is

This repository provides the shared building blocks that underpin Zero-OS, Zero-OS v4, and Zero-OS Light. Rather than duplicating low-level logic across each OS variant, ZOS Base centralizes hardware abstraction, common protocols, and reusable libraries. This ensures consistency, reduces maintenance burden, and makes it easier to evolve the operating system family as a whole.

## What this repository contains

- **Hardware abstraction layer** — CPU, memory, and device interfaces
- **Networking primitives** — common network setup, wireguard, and routing utilities
- **Storage operations** — volume, cache, and filesystem abstractions
- **Shared protocols and data types** — reservation formats, capacity structures, and wire formats
- **Documentation and specifications** — design docs, FAQs, and internal architecture documentation
- **Common test utilities and development helpers**

## Role in the stack

ZOS Base sits at the bottom of the Zero-OS software stack. It is imported and used by:

- **ZOS** — the main Zero-OS V2 node operating system
- **ZOS v4** — the next-generation Zero-OS V4
- **ZOS Light** — the lightweight variant for edge and constrained devices

Any improvement or fix in ZOS Base propagates to all dependent OS variants, making it the central point of reuse for the Zero-OS ecosystem.

## ZOS / Zero-OS

ZOS, also known as Zero-OS, is the operating system layer used to run and manage nodes. It provides the low-level runtime environment for workloads, networking, storage, and automation. This repository supplies the shared libraries and abstractions that make all Zero-OS variants possible.

## Relation to ThreeFold

This technology is used within the ThreeFold ecosystem and was first deployed on the ThreeFold Grid. The component itself is designed as reusable infrastructure technology and should be understood by its technical function first, independent of any specific deployment.

## Ownership

This repository is owned and maintained by TF-Tech NV, a Belgian company responsible for the development and maintenance of this technology.

## Documentation

Start exploring the codebase by first checking the [documentation](/docs) and [specification documents](/specs).

An [FAQ](./docs/faq/readme.md) is also available for common questions.

## Setting up your development environment

If you want to contribute, read the [contribution guideline](CONTRIBUTING.md) and the documentation to set up your [development environment](qemu/README.md).

## Grid Networks

Zero-OS is deployed on several network environments:

- **production network**: Released stable versions. Used to run the real grid. Cannot be reset. Only stable and battle-tested features reach this level. [Dashboard](https://dashboard.grid.tf/)
- **test network**: Mostly stable features that need to be tested at scale. Can be reset occasionally. [Dashboard](https://dashboard.test.grid.tf/)
- **QA network**: Internal testing of new features. Can be behind development. [Dashboard](https://dashboard.qa.grid.tf/)
- **dev network**: Ephemeral network for developing and testing new features. Can be created and reset at any time. [Dashboard](https://dashboard.dev.grid.tf/)

Learn more about the different networks by reading the [upgrade documentation](/docs/internals/identity/upgrade.md#philosophy).

### Provisioning of workloads

Zero-OS does not expose an interface. Instead, it waits for reservations to happen on a trusted source, and once a reservation is available, the node applies it to reality. You can start reading about [provisioning](./docs/provision) in this document.

## Community

If you have questions or want to connect, you can find the community on:

- Telegram: <https://t.me/zero_os_tech>
- Matrix: #zero-os:matrix.org

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
