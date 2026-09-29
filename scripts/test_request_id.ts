import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import { newRequestId } from '../packages/request-id/index';
const descriptor = Object.getOwnPropertyDescriptor(globalThis,'crypto');
try {
  Object.defineProperty(globalThis,'crypto',{ configurable:true, value:{getRandomValues:webcrypto.getRandomValues.bind(webcrypto)} });
  const ids = new Set(Array.from({length:1000},()=>newRequestId()));
  assert.equal(ids.size,1000);
  assert.ok([...ids].every(id=>/^[a-f0-9]{32}$/.test(id)));
  Object.defineProperty(globalThis,'crypto',{ configurable:true, value:undefined });
  assert.throws(()=>newRequestId(),/浏览器/);
  console.log('PASS HTTP request IDs, uniqueness and unavailable crypto');
} finally {
  if (descriptor) Object.defineProperty(globalThis,'crypto',descriptor);
  else Reflect.deleteProperty(globalThis,'crypto');
}
