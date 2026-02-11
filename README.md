# entropy-service

### Theoretical purposes
GO-based software intended to let users fetch randomness via simple API(s).
Behind my setup, enrtopy is provided by a real QRNG, Quantum Random Number Generator, made by ID Quantique in Geneva. The software can be easily adapted to feed entropy from different sources, even Linux PRNG or an USB Chaos Key, for example.

### Practical implementations
With this simple yet very performant software, Users can setup their own cryptographic randomness source, and use API(s) to retrieve different amounts of binary randomness, randomly-generated images, amd sounds (later feature to be added soon).

### GO programming techniques and logics implemented, including:
- socket management
- HTTP and HTTPS servers sharing same mux
- thread-safe and thread-aware structs
- entropy counters (buffer, pressure, reseed interval, reseed bits etc)
- running multiple routines in a context-safe manner, correctly implementing and supporting OS-signalling
- ...

### What is (yet) missing:
- CLI options
- OS Variables to enable/disable TLS, h2, and other base concepts
- systemd implementation, to have it startup at boot, eventually after inserting or at leas probing, the proper kernel module to support the RNG source
- CUDA-awarness and integration if interesting or found to be relevant.
- 

### GO build and run
In the base directory, simply run:
```
go vet
go fmt
go build
```
NB: GO may hint and complain about the lack of several imported libraries from main.go. Will try to summarize those here to save your time, soon.

THe below simple invocation command will spinup two listeners, on all available interfaces, the unsecured HTTP on port 8080, the HTTPS one on 8443.
```./entropy-service &```
or alternatively
```go run entropy-service```

### Mature PoC
The whole project is just a showcase and PoC using very very old PCI-not-Express motherboard, an old-unsuppoorted QRNG card by ID Quantique (as support ended with Kernel 4, I had to migrate some calls to make it compile on Kernel(s) 5 and 6. PC is equipped with a very old Core Duo 2, having only two cores, about 3Ghz and a bus limited to 3Gbit (I believe is the old PCI bandwidth). Nonetheless, the software was easily able to respond up to 3'500 requests per second, reaching an impressive bandwidth of 270MB/s on that hardware dating ~2012.



