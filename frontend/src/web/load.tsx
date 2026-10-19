/** Download, decrypt and validate the object a relay page points at. */
import { msg, t } from "../i18n";
import { EnvelopeError, openEnvelope } from "./envelope";
import { PAYLOAD_FORMAT_VERSION, payloadVersion } from "./payloads";
import { associatedData, readRelayObject, type RelayAddress } from "./relay";
import { SnapshotRejectedError } from "./snapshotSession";

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
  data: {
    title: msg("The received data cannot be shown"),
    detail: msg(
      "It is incomplete or not supported by this version of Yousage. Try again after the desktop app syncs, or update the apps so both match.",
    ),
  },
  display: {
    title: msg("This page cannot be shown"),
    detail: msg(
      "Something went wrong while showing the data. Try again; your link and saved data are kept.",
    ),
  },
} as const satisfies Record<string, RelayProblemText>;

/**
 * The error a viewer refresh reports for a problem.
 *
 * @param problem - Why the newer snapshot could not be opened.
 * @returns An error whose message is the problem's title; data and version problems persist
 * until the desktop sends another snapshot, so even quiet refreshes report them.
 */
export function refreshError(problem: keyof typeof PROBLEMS): Error {
  const message = t(PROBLEMS[problem].title);
  return problem === "data" || problem === "version"
    ? new SnapshotRejectedError(message)
    : new Error(message);
}

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
 * @returns The payload, a version problem for another format version, or a data problem
 * for an opened payload missing what the page reads.
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
        payloadVersion(value) !== PAYLOAD_FORMAT_VERSION ? "version" : "data",
    };
  return { status: "ok", payload: value, etag };
}
