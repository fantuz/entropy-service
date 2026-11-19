import assert from "node:assert/strict";
import {createRequire} from "node:module";
import {X509Certificate, createPrivateKey, webcrypto} from "node:crypto";

const require = createRequire(import.meta.url);
const {generate, pem, names} = require("../cmd/entropy-server/web/certificate-crypto.js");
const seed = new Uint8Array(64).fill(42);
const now = Date.UTC(2026, 10, 19, 12);
const provider = value => ({subtle: webcrypto.subtle,
    getRandomValues(bytes) { return bytes.fill(value); }});

async function certificate(server = seed, local = 7, options = {}) {
    const generated = await generate(server, {now, crypto: provider(local), ...options});
    const parsed = new X509Certificate(generated.certificate);
    const privateKey = createPrivateKey({key: Buffer.from(await webcrypto.subtle.exportKey("pkcs8", generated.privateKey)),
        format: "der", type: "pkcs8"});
    assert.ok(parsed.verify(parsed.publicKey), "X.509 self-signature must verify independently");
    assert.ok(parsed.checkPrivateKey(privateKey), "Downloaded private key must match the certificate");
    assert.equal(parsed.ca, false);
    assert.deepEqual(parsed.keyUsage, ["1.3.6.1.5.5.7.3.1"], "Only TLS server authentication is allowed");
    assert.ok(parsed.serialNumber.length <= 40 && BigInt("0x" + parsed.serialNumber) > 0n);
    assert.equal(parsed.fingerprint256, generated.fingerprint);
    const timestamp = Math.floor((options.now ?? now) / 1000) * 1000;
    assert.equal(new Date(parsed.validFrom).getTime(), timestamp - 300000);
    assert.equal(new Date(parsed.validTo).getTime(), timestamp + (options.days ?? 7) * 86400000);
    assert.equal(new Date(generated.notBefore).getTime(), new Date(parsed.validFrom).getTime());
    assert.equal(new Date(generated.notAfter).getTime(), new Date(parsed.validTo).getTime());
    assert.equal(new X509Certificate(pem("CERTIFICATE", generated.certificate)).fingerprint256, generated.fingerprint);
    return {generated, parsed, privateKey, publicKey: parsed.publicKey.export({format: "der", type: "spki"})};
}

const first = await certificate();
await certificate(seed, 7, {now: now + 789});
assert.equal(first.parsed.checkHost("localhost"), "localhost");
assert.equal(first.parsed.checkIP("127.0.0.1"), "127.0.0.1");
assert.equal(first.parsed.checkIP("::1"), "::1");
assert.equal(first.parsed.checkHost("unlisted.invalid"), undefined);
assert.deepEqual((await certificate()).publicKey, first.publicKey, "Fixed contributions select the same key");
assert.notDeepEqual((await certificate(new Uint8Array(64).fill(43))).publicKey, first.publicKey,
    "The server contribution must affect the private key");
assert.notDeepEqual((await certificate(seed, 8)).publicKey, first.publicKey,
    "The independent browser contribution must affect the private key");
assert.ok(seed.every(value => value === 42), "Caller owns the server byte buffer");

// Exercise rejection boundaries deterministically while leaving public-point
// derivation and signing with the real native provider.
const order = Buffer.from("ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551", "hex");
let attempts = 0;
function rejectionProvider(alwaysInvalid = false) {
    return {getRandomValues: bytes => bytes.fill(9), subtle: new Proxy(webcrypto.subtle, {
        get(target, property) {
            if (property === "deriveBits") return async (algorithm, key, bits) => {
                const label = new TextDecoder().decode(algorithm.info);
                if (!label.startsWith("entropy-service:certificate-key:")) return target.deriveBits(algorithm, key, bits);
                attempts++;
                if (alwaysInvalid || attempts === 1) return new ArrayBuffer(32);
                if (attempts === 2) return Uint8Array.from(order).buffer;
                const scalar = new Uint8Array(32); scalar[31] = 1; return scalar.buffer;
            };
            const value = Reflect.get(target, property);
            return typeof value === "function" ? value.bind(target) : value;
        },
    })};
}
const boundary = new X509Certificate((await generate(seed, {now, crypto: rejectionProvider()})).certificate);
assert.equal(attempts, 3, "Zero and the group order must be rejected");
assert.equal(Buffer.from(boundary.publicKey.export({format: "jwk"}).x, "base64url").toString("hex"),
    "6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296", "Scalar one must derive the standard generator");
assert.ok(boundary.verify(boundary.publicKey));
attempts = 0;
await assert.rejects(generate(seed, {now, crypto: rejectionProvider(true)}), /valid P-256 key/);
assert.equal(attempts, 128, "A failing provider must not loop indefinitely");

const international = await certificate(seed, 7, {commonName: "Service α", days: 365,
    names: ["*.example.org", "bücher.example", "2001:db8::7", "::ffff:192.0.2.4", "LOCALHOST.", "localhost"]});
assert.equal(international.parsed.checkHost("api.example.org"), "*.example.org");
assert.equal(international.parsed.checkHost("xn--bcher-kva.example"), "xn--bcher-kva.example");
assert.equal(international.parsed.checkIP("2001:db8::7"), "2001:db8::7");
assert.equal(international.generated.names.filter(value => value === "localhost").length, 1);
const future = (await certificate(seed, 7, {now: Date.UTC(2050, 0, 1)})).parsed;
assert.ok(future.verify(future.publicKey));
assert.equal(new Date(future.validTo).getUTCFullYear(), 2050);

for (const input of [[], Array(33).fill("a.example"), [17], [""], ["https://example.org"],
    ["example.org/"], ["example.org?"], ["example.org#"], ["user@example.org"], ["example.org\\"],
    ["127.01.0.1"], ["256.0.0.1"], ["0x7f.1"], ["123"], ["bad_name"], ["a..b"],
    ["a".repeat(64) + ".org"], ["*.*.org"], ["[::1]:80"], ["::1%eth0"], ["[::1]/"]]) {
    assert.throws(() => names(input), undefined, JSON.stringify(input));
}
for (const options of [{days: 0}, {days: 366}, {days: 1.5}, {now: NaN}, {now: Date.UTC(2100, 0)},
    {commonName: "é".repeat(33)}, {commonName: "bad\nname"}, {commonName: " "}]) {
    await assert.rejects(generate(seed, {...options, crypto: provider(7)}));
}
for (const input of [new Uint8Array(63), new Uint8Array(65), Array(64).fill(1)]) {
    await assert.rejects(generate(input), /64 server bytes/);
}
await assert.rejects(generate(seed, {crypto: {}}), /Web Crypto/);
await assert.rejects(generate(seed, {crypto: {getRandomValues: bytes => bytes.fill(1),
    subtle: {...webcrypto.subtle, importKey: () => Promise.reject(new Error("Provider unavailable"))}}}));
assert.throws(() => pem("PUBLIC KEY", new Uint8Array(8)), /Unsupported PEM/);
console.log("Certificate crypto: native key mixing, X.509 signatures, SANs, validity, usage and input bounds passed");
