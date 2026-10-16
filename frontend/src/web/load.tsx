/** Download, decrypt and validate the object a relay page points at. */
import { msg } from "../i18n";
import { EnvelopeError, openEnvelope } from "./envelope";
import { PAYLOAD_FORMAT_VERSION, payloadVersion } from "./payloads";
import { associatedData, readRelayObject, type RelayAddress } from "./relay";

/** A failure explained to the person who opened the link. */
export type RelayProblemText = { title: string; detail: string };

/** Message keys for every way a relay page can fail to open. */
export const PROBLEMS = {
  link: {
    title: msg("This link is incomplete"),
    detail: msg(
      "The part after # holds the key that opens this page. Copy the whole link or scan the code again.",
    ),
  },
  missing: {
    title: msg("This page is no longer available"),
    detail: msg("It has expired, was revoked, or the link was replaced."),
  },
  decrypt: {
    title: msg("This page cannot be opened"),
    detail: msg(
      "The key does not match the stored data. Copy the whole link or scan the code again.",
    ),
  },
  version: {
    title: msg("Versions do not match"),
    detail: msg(
      "This page was created by a different Yousage version. Update the relay or the desktop app so both match.",
    ),
  },
  network: {
    title: msg("The relay cannot be reached"),
    detail: msg("Check your connection and try again."),
  },
} as const satisfies Record<string, RelayProblemText>;

/** A loaded payload with its entity tag, or the problem that stopped it. */
export type LoadResult<T> =
  | { status: "ok"; payload: T; etag: string }
  | { status: "unchanged" }
  | { status: "problem"; problem: keyof typeof PROBLEMS };

/**
 * Read and open the addressed relay object.
 *
 * @param address - Object and key from the page URL.
 * @param isValid - Shape check for the expected payload kind.
 * @param etag - Entity tag of the payload already shown.
 * @returns The payload, an unchanged marker, or a problem key.
 */
export async function loadRelayPayload<T>(
  address: RelayAddress,
  isValid: (value: unknown) => value is T,
  etag = "",
): Promise<LoadResult<T>> {
  let read;
  try {
    read = await readRelayObject(address, etag);
  } catch {
    return { status: "problem", problem: "network" };
  }
  if (read.status === "unchanged") return { status: "unchanged" };
  if (read.status === "missing")
    return { status: "problem", problem: "missing" };
  let value: unknown;
  try {
    value = await openEnvelope(
      read.envelope,
      address.key,
      associatedData(address),
    );
  } catch (cause) {
    const failure = cause instanceof EnvelopeError ? cause.failure : "decrypt";
    return {
      status: "problem",
      problem:
        failure === "key"
          ? "link"
          : failure === "version"
            ? "version"
            : "decrypt",
    };
  }
  return checkPayload(value, isValid, read.etag);
}

/**
 * Accept a decrypted payload of the expected kind and format version.
 *
 * @param value - Parsed payload JSON.
 * @param isValid - Shape check for the expected payload kind.
 * @param etag - Entity tag the payload was served with.
 * @returns The payload, or a version or decrypt problem.
 */
export function checkPayload<T>(
  value: unknown,
  isValid: (value: unknown) => value is T,
  etag: string,
): LoadResult<T> {
  if (!isValid(value))
    return {
      status: "problem",
      problem:
        payloadVersion(value) !== PAYLOAD_FORMAT_VERSION
          ? "version"
          : "decrypt",
    };
  return { status: "ok", payload: value, etag };
}
