# entropy-service

GO-based software intended to let users fetch randomness via simple API(s).

Behind my setup, enrtopy is provided by a real QRNG, Quantum Random Number Generator, made by ID Quantique in Geneva. The software can be easily adapted to feed entropy from different sources, even Linux PRNG or an USB Chaos Key, for example.

With this simple yet very performant software, Users can setup their own cryptographic randomness source, and use API(s) to retrieve different amounts of binary randomness, randomly-generated images, amd sounds (later feature to be added soon).

The whole project is just a showcase and PoC using very very old PCI-not-Express motherboard, an old-unsuppoorted QRNG card by ID Quantique (as support ended with Kernel 4, I had to migrate some calls to make it compile on Kernel(s) 5 and 6. PC is equipped with a very old Core Duo 2, having only two cores, about 3Ghz and a bus limited to 3Gbit (I believe is the old PCI bandwidth). Nonetheless, the software was easily able to respond up to 3'500 requests per second, reaching an impressive bandwidth of 270MB/s on that hardware dating ~2012.

This PoC demonstrates the usage of different GO programming techniques, including:
- sockets spinup
- HTTP and HTTPS servers sharing same mux
- thread-safe and thread-aware structs
- entropy counters (buffer, pressure, reseed interval, reseed bits etc)
- running multiple routines in a context-safe manner, correctly implementing and supporting OS-signalling
- ...
