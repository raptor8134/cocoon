// Browser-side persistence: the draft autosave.
//
// This exists for the same reason the Go version did: the page can be dropped
// at any time -- tab close, crash, or the service worker reloading onto a new
// deploy -- and none of those are the user's choice. Every edit is written
// here and restored on load, so a reload puts you back exactly where you were.
//
// IndexedDB rather than localStorage: configs are small today, but the planned
// "bucket" (winds + CSV profiles + settings) will not be, and localStorage is
// a synchronous 5MB string store. Starting here avoids a migration later.
//
// Caveat worth remembering: IndexedDB is evictable. Browsers clear it under
// storage pressure, and Safari drops it for sites unvisited for 7 days. This
// is a safety net, NOT the system of record -- that is the file the user saves.

const DB_NAME = "cocoon";
const DB_VERSION = 1;
const STORE = "kv";
const DRAFT_KEY = "draft";

let dbPromise = null;

function open() {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE)) db.createObjectStore(STORE);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
}

async function put(key, value) {
  const db = await open();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).put(value, key);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
}

async function get(key) {
  const db = await open();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, "readonly");
    const req = tx.objectStore(STORE).get(key);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

/**
 * Persist the working buffer. Failures are swallowed deliberately: autosave is
 * a safety net, and an error toast on every keystroke would be worse than a
 * missed write. A genuinely broken store surfaces on an explicit Save.
 */
export async function saveDraft(draft) {
  try {
    await put(DRAFT_KEY, { ...draft, savedAt: Date.now() });
  } catch (err) {
    console.warn("cocoon: draft autosave failed", err);
  }
}

export async function loadDraft() {
  try {
    const d = await get(DRAFT_KEY);
    return d && d.cfg ? d : null;
  } catch (err) {
    console.warn("cocoon: draft restore failed", err);
    return null;
  }
}

export async function clearDraft() {
  try {
    const db = await open();
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).delete(DRAFT_KEY);
  } catch (err) {
    console.warn("cocoon: draft clear failed", err);
  }
}
