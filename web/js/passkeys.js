// Browser side of WebAuthn: base64url conversion and the two ceremonies.

const b64urlToBytes = (value) => {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(padded + '='.repeat((4 - (padded.length % 4)) % 4));
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
};

const bytesToB64url = (buffer) => {
  const bytes = new Uint8Array(buffer);
  let raw = '';
  for (let i = 0; i < bytes.length; i++) raw += String.fromCharCode(bytes[i]);
  return btoa(raw).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
};

export const supported = () =>
  typeof window.PublicKeyCredential === 'function' &&
  !!(navigator.credentials && navigator.credentials.create);

/** True when the platform has a built-in authenticator (Touch ID, Windows Hello…). */
export async function platformAvailable() {
  try {
    if (!supported() || !window.PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable) return false;
    return await window.PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable();
  } catch {
    return false;
  }
}

function decodeCreationOptions(options) {
  const out = { ...options };
  out.challenge = b64urlToBytes(options.challenge);
  out.user = { ...options.user, id: b64urlToBytes(options.user.id) };
  out.excludeCredentials = (options.excludeCredentials || []).map((c) => ({
    ...c,
    id: b64urlToBytes(c.id),
  }));
  return out;
}

function decodeRequestOptions(options) {
  const out = { ...options };
  out.challenge = b64urlToBytes(options.challenge);
  out.allowCredentials = (options.allowCredentials || []).map((c) => ({
    ...c,
    id: b64urlToBytes(c.id),
  }));
  return out;
}

/** Run the registration ceremony and return the JSON DocMan expects. */
export async function register(options) {
  const credential = await navigator.credentials.create({ publicKey: decodeCreationOptions(options) });
  if (!credential) throw new Error('the browser did not return a passkey');
  const response = credential.response;
  let transports = [];
  if (typeof response.getTransports === 'function') {
    try { transports = response.getTransports() || []; } catch { transports = []; }
  }
  return {
    id: credential.id,
    rawId: bytesToB64url(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment || '',
    response: {
      clientDataJSON: bytesToB64url(response.clientDataJSON),
      attestationObject: bytesToB64url(response.attestationObject),
      transports,
    },
  };
}

/** Run the sign-in ceremony and return the JSON DocMan expects. */
export async function authenticate(options) {
  const credential = await navigator.credentials.get({
    publicKey: decodeRequestOptions(options),
    mediation: 'optional',
  });
  if (!credential) throw new Error('no passkey was chosen');
  const response = credential.response;
  return {
    id: credential.id,
    rawId: bytesToB64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bytesToB64url(response.clientDataJSON),
      authenticatorData: bytesToB64url(response.authenticatorData),
      signature: bytesToB64url(response.signature),
      userHandle: response.userHandle ? bytesToB64url(response.userHandle) : '',
    },
  };
}

/** Turn a WebAuthn DOMException into something worth reading. */
export function describeError(err) {
  const name = err && err.name;
  switch (name) {
    case 'NotAllowedError':
      return 'The passkey prompt was dismissed or timed out.';
    case 'InvalidStateError':
      return 'This device already has a passkey registered for DocMan.';
    case 'SecurityError':
      return 'The browser refused: passkeys need HTTPS and a hostname that matches the site.';
    case 'AbortError':
      return 'The passkey request was cancelled.';
    case 'NotSupportedError':
      return 'This browser or device cannot create the kind of passkey DocMan asked for.';
    default:
      return (err && err.message) || 'The passkey request failed.';
  }
}
