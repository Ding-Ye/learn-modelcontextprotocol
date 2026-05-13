/* Resources */
/**
 * Sent from the client to request a list of resources the server has.
 *
 * @category `resources/list`
 */
export interface ListResourcesRequest extends PaginatedRequest {
  method: "resources/list";
}

/**
 * The server's response to a resources/list request from the client.
 *
 * @category `resources/list`
 */
export interface ListResourcesResult extends PaginatedResult {
  resources: Resource[];
}

/**
 * Sent from the client to request a list of resource templates the server has.
 *
 * @category `resources/templates/list`
 */
export interface ListResourceTemplatesRequest extends PaginatedRequest {
  method: "resources/templates/list";
}

/**
 * The server's response to a resources/templates/list request from the client.
 *
 * @category `resources/templates/list`
 */
export interface ListResourceTemplatesResult extends PaginatedResult {
  resourceTemplates: ResourceTemplate[];
}

/**
 * Common parameters when working with resources.
 *
 * @internal
 */
export interface ResourceRequestParams extends RequestParams {
  /**
   * The URI of the resource. The URI can use any protocol; it is up to the server how to interpret it.
   *
   * @format uri
   */
  uri: string;
}

/**
 * Parameters for a `resources/read` request.
 *
 * @category `resources/read`
 */
// eslint-disable-next-line @typescript-eslint/no-empty-object-type
export interface ReadResourceRequestParams extends ResourceRequestParams {}

/**
 * Sent from the client to the server, to read a specific resource URI.
 *
 * @category `resources/read`
 */
export interface ReadResourceRequest extends JSONRPCRequest {
  method: "resources/read";
  params: ReadResourceRequestParams;
}

/**
 * The server's response to a resources/read request from the client.
 *
 * @category `resources/read`
 */
export interface ReadResourceResult extends Result {
  contents: (TextResourceContents | BlobResourceContents)[];
}

/**
 * An optional notification from the server to the client, informing it that the list of resources it can read from has changed. This may be issued by servers without any previous subscription from the client.
 *
 * @category `notifications/resources/list_changed`
 */
export interface ResourceListChangedNotification extends JSONRPCNotification {
  method: "notifications/resources/list_changed";
  params?: NotificationParams;
}

/**
 * Parameters for a `resources/subscribe` request.
 *
 * @category `resources/subscribe`
 */
// eslint-disable-next-line @typescript-eslint/no-empty-object-type
export interface SubscribeRequestParams extends ResourceRequestParams {}

/**
 * Sent from the client to request resources/updated notifications from the server whenever a particular resource changes.
 *
 * @category `resources/subscribe`
 */
export interface SubscribeRequest extends JSONRPCRequest {
  method: "resources/subscribe";
  params: SubscribeRequestParams;
}

/**
 * Parameters for a `resources/unsubscribe` request.
 *
 * @category `resources/unsubscribe`
 */
// eslint-disable-next-line @typescript-eslint/no-empty-object-type
export interface UnsubscribeRequestParams extends ResourceRequestParams {}

/**
 * Sent from the client to request cancellation of resources/updated notifications from the server. This should follow a previous resources/subscribe request.
 *
 * @category `resources/unsubscribe`
 */
export interface UnsubscribeRequest extends JSONRPCRequest {
  method: "resources/unsubscribe";
  params: UnsubscribeRequestParams;
}

/**
 * Parameters for a `notifications/resources/updated` notification.
 *
 * @category `notifications/resources/updated`
 */
export interface ResourceUpdatedNotificationParams extends NotificationParams {
  /**
   * The URI of the resource that has been updated. This might be a sub-resource of the one that the client actually subscribed to.
   *
   * @format uri
   */
  uri: string;
}

/**
 * A notification from the server to the client, informing it that a resource has changed and may need to be read again. This should only be sent if the client previously sent a resources/subscribe request.
 *
 * @category `notifications/resources/updated`
 */
export interface ResourceUpdatedNotification extends JSONRPCNotification {
  method: "notifications/resources/updated";
  params: ResourceUpdatedNotificationParams;
}

/**
 * A known resource that the server is capable of reading.
 *
 * @category `resources/list`
 */
export interface Resource extends BaseMetadata, Icons {
  /**
   * The URI of this resource.
   *
   * @format uri
   */
  uri: string;

  /**
   * A description of what this resource represents.
   *
   * This can be used by clients to improve the LLM's understanding of available resources. It can be thought of like a "hint" to the model.
   */
  description?: string;

  /**
   * The MIME type of this resource, if known.
   */
  mimeType?: string;

  /**
   * Optional annotations for the client.
   */
  annotations?: Annotations;

  /**
   * The size of the raw resource content, in bytes (i.e., before base64 encoding or any tokenization), if known.
   *
   * This can be used by Hosts to display file sizes and estimate context window usage.
   */
  size?: number;

  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: { [key: string]: unknown };
}

/**
 * A template description for resources available on the server.
 *
 * @category `resources/templates/list`
 */
export interface ResourceTemplate extends BaseMetadata, Icons {
  /**
   * A URI template (according to RFC 6570) that can be used to construct resource URIs.
   *
   * @format uri-template
   */
  uriTemplate: string;

  /**
   * A description of what this template is for.
   *
   * This can be used by clients to improve the LLM's understanding of available resources. It can be thought of like a "hint" to the model.
   */
  description?: string;

  /**
   * The MIME type for all resources that match this template. This should only be included if all resources matching this template have the same type.
   */
  mimeType?: string;

  /**
   * Optional annotations for the client.
   */
  annotations?: Annotations;

  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: { [key: string]: unknown };
}

/**
 * The contents of a specific resource or sub-resource.
 *
 * @internal
 */
export interface ResourceContents {
  /**
   * The URI of this resource.
   *
   * @format uri
   */
  uri: string;
  /**
   * The MIME type of this resource, if known.
   */
  mimeType?: string;

  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: { [key: string]: unknown };
}

/**
 * @category Content
 */
export interface TextResourceContents extends ResourceContents {
  /**
   * The text of the item. This must only be set if the item can actually be represented as text (not binary data).
   */
  text: string;
}

/**
 * @category Content
 */
export interface BlobResourceContents extends ResourceContents {
  /**
   * A base64-encoded string representing the binary data of the item.
   *
   * @format byte
   */
  blob: string;
}
