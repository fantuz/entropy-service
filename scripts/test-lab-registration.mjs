import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import vm from "node:vm";
const context=vm.createContext({URL,TextEncoder});
vm.runInContext(readFileSync(new URL("../cmd/entropy-server/web/lab-registration.js",import.meta.url),"utf8"),context);
const {build,configuration}=context.EntropyLabRegistration;
const page={id:"page",kind:"webpage",endpoint:"https://example.org/",max_bytes:4096,timeout_ms:5000,urls:"https://example.net/\nhttps://example.com/"};
assert.equal(build(page).urls.length,2);
for(const input of [
 {...page,id:"Bad ID"},{...page,max_bytes:0},{...page,timeout_ms:99},
 {...page,endpoint:"https://user:fixture@example.org"},{...page,urls:"file:///etc/passwd"},
 {...page,endpoint:"https://example.org/lab/stream"},{...page,urls:Array(17).fill("https://example.org/")},
 {...page,kind:"entropy-ws",endpoint:"https://example.org/path"},
])assert.throws(()=>build(input));
const camera=build({...page,kind:"onvif",endpoint:"http://camera/onvif/device_service",width:160,height:120,profile_token:"profile-1"});
assert.equal(camera.max_bytes,57600);assert.equal(camera.profile_token,"profile-1");
assert.equal(configuration({sources:[camera]}).sources[0].width,160);
assert.equal(build({...page,kind:"tcp",endpoint:"127.0.0.1:9000"}).endpoint,"127.0.0.1:9000");
for(const value of [{sources:[page,page]},{sources:[{...page,password:"fixture-only"}]},{sources:Array(33).fill(page)},{wrong:[]},null])assert.throws(()=>configuration(value));
console.log("Registration validation, camera sizing, URL pools, import limits and credential exclusion: passed");
