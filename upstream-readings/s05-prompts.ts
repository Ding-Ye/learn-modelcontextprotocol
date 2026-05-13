/* Prompts */
/**
 * Sent from the client to request a list of prompts and prompt templates the server has.
 *
 * @category `prompts/list`
 */
export interface ListPromptsRequest extends PaginatedRequest {
  method: "prompts/list";
}

/**
 * The server's response to a prompts/list request from the client.
 *
 * @category `prompts/list`
 */
export interface ListPromptsResult extends PaginatedResult {
  prompts: Prompt[];
}

/**
 * Parameters for a `prompts/get` request.
 *
 * @category `prompts/get`
 */
export interface GetPromptRequestParams extends RequestParams {
  /**
   * The name of the prompt or prompt template.
   */
  name: string;
  /**
   * Arguments to use for templating the prompt.
   */
  arguments?: { [key: string]: string };
}

/**
 * Used by the client to get a prompt provided by the server.
 *
 * @category `prompts/get`
 */
export interface GetPromptRequest extends JSONRPCRequest {
  method: "prompts/get";
  params: GetPromptRequestParams;
}

/**
 * The server's response to a prompts/get request from the client.
 *
 * @category `prompts/get`
 */
export interface GetPromptResult extends Result {
  /**
   * An optional description for the prompt.
   */
  description?: string;
  messages: PromptMessage[];
}

/**
 * A prompt or prompt template that the server offers.
 *
 * @category `prompts/list`
 */
export interface Prompt extends BaseMetadata, Icons {
  /**
   * An optional description of what this prompt provides
   */
  description?: string;

  /**
   * A list of arguments to use for templating the prompt.
   */
  arguments?: PromptArgument[];

  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: { [key: string]: unknown };
}

/**
 * Describes an argument that a prompt can accept.
 *
 * @category `prompts/list`
 */
export interface PromptArgument extends BaseMetadata {
  /**
   * A human-readable description of the argument.
   */
  description?: string;
  /**
   * Whether this argument must be provided.
   */
  required?: boolean;
}

/**
 * The sender or recipient of messages and data in a conversation.
 *
 * @category Common Types
 */
export type Role = "user" | "assistant";

/**
 * Describes a message returned as part of a prompt.
 *
 * This is similar to `SamplingMessage`, but also supports the embedding of
 * resources from the MCP server.
 *
 * @category `prompts/get`
 */
export interface PromptMessage {
  role: Role;
  content: ContentBlock;
}

/**
 * A resource that the server is capable of reading, included in a prompt or tool call result.
 *
 * Note: resource links returned by tools are not guaranteed to appear in the results of `resources/list` requests.
 *
 * @category Content
 */
export interface ResourceLink extends Resource {
  type: "resource_link";
}

/**
 * The contents of a resource, embedded into a prompt or tool call result.
 *
 * It is up to the client how best to render embedded resources for the benefit
 * of the LLM and/or the user.
 *
 * @category Content
 */
export interface EmbeddedResource {
  type: "resource";
  resource: TextResourceContents | BlobResourceContents;

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
 * An optional notification from the server to the client, informing it that the list of prompts it offers has changed. This may be issued by servers without any previous subscription from the client.
 *
 * @category `notifications/prompts/list_changed`
 */
export interface PromptListChangedNotification extends JSONRPCNotification {
  method: "notifications/prompts/list_changed";
  params?: NotificationParams;
}


/* --- completion/complete --- schema.ts:2006-2088 --- */

export interface CompleteRequestParams extends RequestParams {
  ref: PromptReference | ResourceTemplateReference;
  /**
   * The argument's information
   */
  argument: {
    /**
     * The name of the argument
     */
    name: string;
    /**
     * The value of the argument to use for completion matching.
     */
    value: string;
  };

  /**
   * Additional, optional context for completions
   */
  context?: {
    /**
     * Previously-resolved variables in a URI template or prompt.
     */
    arguments?: { [key: string]: string };
  };
}

/**
 * A request from the client to the server, to ask for completion options.
 *
 * @category `completion/complete`
 */
export interface CompleteRequest extends JSONRPCRequest {
  method: "completion/complete";
  params: CompleteRequestParams;
}

/**
 * The server's response to a completion/complete request
 *
 * @category `completion/complete`
 */
export interface CompleteResult extends Result {
  completion: {
    /**
     * An array of completion values. Must not exceed 100 items.
     */
    values: string[];
    /**
     * The total number of completion options available. This can exceed the number of values actually sent in the response.
     */
    total?: number;
    /**
     * Indicates whether there are additional completion options beyond those provided in the current response, even if the exact total is unknown.
     */
    hasMore?: boolean;
  };
}

/**
 * A reference to a resource or resource template definition.
 *
 * @category `completion/complete`
 */
export interface ResourceTemplateReference {
  type: "ref/resource";
  /**
   * The URI or URI template of the resource.
   *
   * @format uri-template
   */
  uri: string;
}

/**
 * Identifies a prompt.
 *
 * @category `completion/complete`
 */
export interface PromptReference extends BaseMetadata {
  type: "ref/prompt";
}

