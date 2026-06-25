import * as openpgp from "openpgp";

export type GeneratedKey = {
  privateKeyArmored: string;
  publicKeyArmored: string;
  fingerprint: string;
};

export async function generateKey(name: string, email: string, passphrase: string): Promise<GeneratedKey> {
  const generated = await openpgp.generateKey({
    type: "ecc",
    curve: "curve25519Legacy",
    userIDs: [{ name: name || email, email }],
    passphrase,
    format: "armored"
  }) as { privateKey: string; publicKey: string };
  const key = await openpgp.readKey({ armoredKey: generated.publicKey });
  return { privateKeyArmored: generated.privateKey, publicKeyArmored: generated.publicKey, fingerprint: key.getFingerprint() };
}

export async function privateKeyMetadata(privateKeyArmored: string): Promise<GeneratedKey> {
  const privateKey = await openpgp.readPrivateKey({ armoredKey: privateKeyArmored });
  const publicKey = privateKey.toPublic();
  const publicKeyArmored = publicKey.armor();
  return { privateKeyArmored, publicKeyArmored, fingerprint: privateKey.getFingerprint() };
}

export function passphraseIssues(passphrase: string, blockedValues: string[]) {
  const issues: string[] = [];
  const lower = passphrase.toLowerCase();
  if (passphrase.length < 14) issues.push("Use at least 14 characters.");
  for (const value of blockedValues) {
    const clean = value.trim().toLowerCase();
    if (clean && clean.length >= 4 && lower.includes(clean)) {
      issues.push(`Avoid using ${value} in the passphrase.`);
      break;
    }
  }
  if (/^(password|passphrase|cairnfield|rolltop|openpgp|letmein)/i.test(passphrase)) issues.push("Avoid obvious passphrases.");
  return issues;
}

export async function encryptText(text: string, publicKeyArmored: string) {
  const encryptionKeys = await openpgp.readKey({ armoredKey: publicKeyArmored });
  const message = await openpgp.createMessage({ text });
  return openpgp.encrypt({ message, encryptionKeys, format: "armored" }) as Promise<string>;
}

export async function decryptText(armored: string, privateKeyArmored: string, passphrase: string) {
  const privateKey = await openpgp.decryptKey({ privateKey: await openpgp.readPrivateKey({ armoredKey: privateKeyArmored }), passphrase });
  const message = await openpgp.readMessage({ armoredMessage: armored });
  const decrypted = await openpgp.decrypt({ message, decryptionKeys: privateKey, format: "utf8" });
  return String(decrypted.data || "");
}

export async function encryptBytes(data: ArrayBuffer, publicKeyArmored: string) {
  const encryptionKeys = await openpgp.readKey({ armoredKey: publicKeyArmored });
  const message = await openpgp.createMessage({ binary: new Uint8Array(data) });
  const encrypted = await openpgp.encrypt({ message, encryptionKeys, format: "binary" });
  return encrypted instanceof Uint8Array ? encrypted : new Uint8Array(encrypted as ArrayBuffer);
}

export async function decryptBytes(data: ArrayBuffer, privateKeyArmored: string, passphrase: string) {
  const privateKey = await openpgp.decryptKey({ privateKey: await openpgp.readPrivateKey({ armoredKey: privateKeyArmored }), passphrase });
  const message = await openpgp.readMessage({ binaryMessage: new Uint8Array(data) });
  const decrypted = await openpgp.decrypt({ message, decryptionKeys: privateKey, format: "binary" });
  return decrypted.data instanceof Uint8Array ? decrypted.data : new Uint8Array(decrypted.data as ArrayBuffer);
}

export async function verifyPrivateKey(privateKeyArmored: string, passphrase: string) {
  await openpgp.decryptKey({ privateKey: await openpgp.readPrivateKey({ armoredKey: privateKeyArmored }), passphrase });
}

export function downloadKey(filename: string, content: string) {
  const blob = new Blob([content], { type: "application/pgp-keys" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
