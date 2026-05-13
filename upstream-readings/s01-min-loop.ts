/* JSON-RPC types */

/**
 * Refers to any valid JSON-RPC object that can be decoded off the wire, or encoded to be sent.
 *
 * @category JSON-RPC
 */
export type JSONRPCMessage =
  | JSONRPCRequest
  | JSONRPCNotification
  | JSONRPCResponse;

/** @internal */
export const LATEST_PROTOCOL_VERSION = "2025-11-25";
/** @internal */
export const JSONRPC_VERSION = "2.0";

/**
 * A progress token, used to associate progress notifications with the original request.
 *
 * @category Common Types
 */
export type ProgressToken = string | number;

/**
 * An opaque token used to represent a cursor for pagination.
 *
 * @category Common Types
 */
export type Cursor = string;

/**
 * Common params for any task-augmented request.
 *
 * @internal
 */
export interface TaskAugmentedRequestParams extends RequestParams {
  /**
   * If specified, the caller is requesting task-augmented execution for this request.
   * The request will return a CreateTaskResult immediately, and the actual result can be
   * retrieved later via tasks/result.
   *
   * Task augmentation is subject to capability negotiation - receivers MUST declare support
   * for task augmentation of specific request types in their capabilities.
   */
  task?: TaskMetadata;
}
/**
 * Common params for any request.
 *
 * @internal
 */
export interface RequestParams {
  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: {
    /**
     * If specified, the caller is requesting out-of-band progress notifications for this request (as represented by notifications/progress). The value of this parameter is an opaque token that will be attached to any subsequent notifications. The receiver is not obligated to provide these notifications.
     */
    progressToken?: ProgressToken;
    [key: string]: unknown;
  };
}

/** @internal */
export interface Request {
  method: string;
  // Allow unofficial extensions of `Request.params` without impacting `RequestParams`.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  params?: { [key: string]: any };
}

/** @internal */
export interface NotificationParams {
  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: { [key: string]: unknown };
}

/** @internal */
export interface Notification {
  method: string;
  // Allow unofficial extensions of `Notification.params` without impacting `NotificationParams`.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  params?: { [key: string]: any };
}

/**
 * @category Common Types
 */
export interface Result {
  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: { [key: string]: unknown };
  [key: string]: unknown;
}

/**
 * @category Common Types
 */
export interface Error {
  /**
   * The error type that occurred.
   */
  code: number;
  /**
   * A short description of the error. The message SHOULD be limited to a concise single sentence.
   */
  message: string;
  /**
   * Additional information about the error. The value of this member is defined by the sender (e.g. detailed error information, nested errors etc.).
   */
  data?: unknown;
}

/**
 * A uniquely identifying ID for a request in JSON-RPC.
 *
 * @category Common Types
 */
export type RequestId = string | number;

/**
 * A request that expects a response.
 *
 * @category JSON-RPC
 */
export interface JSONRPCRequest extends Request {
  jsonrpc: typeof JSONRPC_VERSION;
  id: RequestId;
}

/**
 * A notification which does not expect a response.
 *
 * @category JSON-RPC
 */
export interface JSONRPCNotification extends Notification {
  jsonrpc: typeof JSONRPC_VERSION;
}

/**
 * A successful (non-error) response to a request.
 *
 * @category JSON-RPC
 */
export interface JSONRPCResultResponse {
  jsonrpc: typeof JSONRPC_VERSION;
  id: RequestId;
  result: Result;
}

/**
 * A response to a request that indicates an error occurred.
 *
 * @category JSON-RPC
 */
export interface JSONRPCErrorResponse {
  jsonrpc: typeof JSONRPC_VERSION;
  id?: RequestId;
  error: Error;
}

/**
 * A response to a request, containing either the result or error.
 *
 * @category JSON-RPC
 */
export type JSONRPCResponse = JSONRPCResultResponse | JSONRPCErrorResponse;

// Standard JSON-RPC error codes
export const PARSE_ERROR = -32700;
export const INVALID_REQUEST = -32600;
export const METHOD_NOT_FOUND = -32601;
export const INVALID_PARAMS = -32602;
export const INTERNAL_ERROR = -32603;

