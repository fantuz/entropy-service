import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";
const context=vm.createContext({URL,TextEncoder});
vm.runInContext(readFileSync(new URL("../cmd/entropy-server/web/lab-registration.js",import.meta.url),"utf8"),context);
const {build,configuration}=context.EntropyLabRegistration;
const {schemaFor,pagePresets,httpStatus}=context.EntropyLabRegistration;
const page={id:"page",kind:"webpage",endpoint:"https://example.org/",max_bytes:4096,timeout_ms:5000,urls:"https://example.net/\nhttps://example.com/"};
assert.equal(build(page).urls.length,2);
for(const input of [
 {...page,id:"Bad ID"},{...page,max_bytes:-1},{...page,timeout_ms:99},
 {...page,endpoint:"https://user:fixture@example.org"},{...page,urls:"file:///etc/passwd"},
 {...page,endpoint:"https://example.org/lab/stream"},{...page,urls:Array(17).fill("https://example.org/")},
 {...page,kind:"entropy-ws",endpoint:"https://example.org/path"},
])assert.throws(()=>build(input));
const camera=build({...page,kind:"onvif",endpoint:"http://camera/onvif/device_service",max_bytes:57600,width:160,height:120,profile_token:"profile-1"});
assert.equal(camera.max_bytes,57600);assert.equal(camera.profile_token,"profile-1");
assert.equal(configuration({sources:[camera]}).sources[0].width,160);
assert.equal(build({...page,kind:"tcp",endpoint:"127.0.0.1:9000"}).endpoint,"127.0.0.1:9000");
for(const kind of ["tcp","udp"])for(const endpoint of [`${kind}://127.0.0.1:9001`,"127.0.0.1:0","127.0.0.1:65536","127.0.0.1:9001/"]){
 assert.throws(()=>build({...page,kind,endpoint}),error=>/host:port|address:port/.test(error.message)&&!error.message.toLowerCase().includes("camera"));
}
assert.equal(build({...page,kind:"websocket",endpoint:schemaFor("websocket").example}).kind,"websocket");
assert.throws(()=>build({...page,kind:"websocket",endpoint:"http://127.0.0.1:8081/stream"}),error=>error.message.includes("ws://")&&!error.message.includes("camera"));
assert.throws(()=>build({...page,kind:"entropy-ws",endpoint:"ws://127.0.0.1:8081/stream"}),error=>error.message.includes("http://127.0.0.1:8081")&&!error.message.includes("camera"));
assert.throws(()=>build({...page,kind:"http",endpoint:"https://example.org/#x"}),error=>error.message.includes("fragment")&&!error.message.includes("camera"));
assert.ok(schemaFor("onvif").example.endsWith("/onvif/device_service"));
assert.throws(()=>schemaFor("__proto__"));
assert.equal(pagePresets.length,7);assert.equal(new Set(pagePresets.map(p=>p.url)).size,7);
for(const preset of pagePresets)assert.equal(build({...page,endpoint:preset.url}).endpoint,preset.url);
assert.match(httpStatus(201),/201 Created.*not a redirect/);
assert.match(httpStatus(200,2),/2 redirect/);assert.ok(!httpStatus(200,true).includes("true"));
for(const value of [{sources:[build(page),build(page)]},{sources:[{...build(page),password:"fixture-only"}]},{sources:Array(33).fill(build(page))},{wrong:[]},null])assert.throws(()=>configuration(value));
const fixtures=JSON.parse(readFileSync(new URL("../cmd/entropy-server/internal/lab/testdata/registration-validation.json",import.meta.url),"utf8"));
for(const fixture of fixtures){
 const validate=()=>configuration({sources:[fixture.source]});
 if(!fixture.valid){assert.throws(validate,undefined,fixture.name);continue;}
 const source=validate().sources[0];
 for(const [key,value] of Object.entries(fixture.normalized))assert.equal(source[key]??0,value,`${fixture.name}: ${key}`);
}
console.log(`${fixtures.length} shared browser/Go validation and defaults cases: passed`);
console.log("Registration validation, camera sizing, URL pools, import limits and credential exclusion: passed");
