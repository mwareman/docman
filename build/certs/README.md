# Extra root certificates for building

Only needed on networks that inspect TLS, such as corporate proxies like
Netskope or Zscaler. Put your root CA here as a `.crt` or `.pem` file, and the
build trusts it while downloading Go modules. It is not copied into the image.

`.crt` and `.pem` files here are ignored by git, because they belong to each
machine. This README keeps the folder in the repository, which the Dockerfile
needs.
