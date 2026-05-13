/* Initialization */
/**
 * Parameters for an `initialize` request.
 *
 * @category `initialize`
 */
export interface InitializeRequestParams extends RequestParams {
  /**
   * The latest version of the Model Context Protocol that the client supports. The client MAY decide to support older versions as well.
   */
  protocolVersion: string;
  capabilities: ClientCapabilities;
  clientInfo: Implementation;
}

/**
 * This request is sent from the client to the server when it first connects, asking it to begin initialization.
 *
 * @category `initialize`
 */
export interface InitializeRequest extends JSONRPCRequest {
  method: "initialize";
  params: InitializeRequestParams;
}

/**
 * After receiving an initialize request from the client, the server sends this response.
 *
 * @category `initialize`
 */
export interface InitializeResult extends Result {
  /**
   * The version of the Model Context Protocol that the server wants to use. This may not match the version that the client requested. If the client cannot support this version, it MUST disconnect.
   */
  protocolVersion: string;
  capabilities: ServerCapabilities;
  serverInfo: Implementation;

  /**
   * Instructions describing how to use the server and its features.
   *
   * This can be used by clients to improve the LLM's understanding of available tools, resources, etc. It can be thought of like a "hint" to the model. For example, this information MAY be added to the system prompt.
   */
  instructions?: string;
}

/**
 * This notification is sent from the client to the server after initialization has finished.
 *
 * @category `notifications/initialized`
 */
export interface InitializedNotification extends JSONRPCNotification {
  method: "notifications/initialized";
  params?: NotificationParams;
}

/**
 * Capabilities a client may support. Known capabilities are defined here, in this schema, but this is not a closed set: any client can define its own, additional capabilities.
 *
 * @category `initialize`
 */
export interface ClientCapabilities {
  /**
   * Experimental, non-standard capabilities that the client supports.
   */
  experimental?: { [key: string]: object };
  /**
   * Present if the client supports listing roots.
   */
  roots?: {
    /**
     * Whether the client supports notifications for changes to the roots list.
     */
    listChanged?: boolean;
  };
  /**
   * Present if the client supports sampling from an LLM.
   */
  sampling?: {
    /**
     * Whether the client supports context inclusion via includeContext parameter.
     * If not declared, servers SHOULD only use `includeContext: "none"` (or omit it).
     */
    context?: object;
    /**
     * Whether the client supports tool use via tools and toolChoice parameters.
     */
    tools?: object;
  };
  /**
   * Present if the client supports elicitation from the server.
   */
  elicitation?: { form?: object; url?: object };

  /**
   * Present if the client supports task-augmented requests.
   */
  tasks?: {
    /**
     * Whether this client supports tasks/list.
     */
    list?: object;
    /**
     * Whether this client supports tasks/cancel.
     */
    cancel?: object;
    /**
     * Specifies which request types can be augmented with tasks.
     */
    requests?: {
      /**
       * Task support for sampling-related requests.
       */
      sampling?: {
        /**
         * Whether the client supports task-augmented sampling/createMessage requests.
         */
        createMessage?: object;
      };
      /**
       * Task support for elicitation-related requests.
       */
      elicitation?: {
        /**
         * Whether the client supports task-augmented elicitation/create requests.
         */
        create?: object;
      };
    };
  };
}

/**
 * Capabilities that a server may support. Known capabilities are defined here, in this schema, but this is not a closed set: any server can define its own, additional capabilities.
 *
 * @category `initialize`
 */
export interface ServerCapabilities {
  /**
   * Experimental, non-standard capabilities that the server supports.
   */
  experimental?: { [key: string]: object };
  /**
   * Present if the server supports sending log messages to the client.
   */
  logging?: object;
  /**
   * Present if the server supports argument autocompletion suggestions.
   */
  completions?: object;
  /**
   * Present if the server offers any prompt templates.
   */
  prompts?: {
    /**
     * Whether this server supports notifications for changes to the prompt list.
     */
    listChanged?: boolean;
  };
  /**
   * Present if the server offers any resources to read.
   */
  resources?: {
    /**
     * Whether this server supports subscribing to resource updates.
     */
    subscribe?: boolean;
    /**
     * Whether this server supports notifications for changes to the resource list.
     */
    listChanged?: boolean;
  };
  /**
   * Present if the server offers any tools to call.
   */
  tools?: {
    /**
     * Whether this server supports notifications for changes to the tool list.
     */
    listChanged?: boolean;
  };
  /**
   * Present if the server supports task-augmented requests.
   */
  tasks?: {
    /**
     * Whether this server supports tasks/list.
     */
    list?: object;
    /**
     * Whether this server supports tasks/cancel.
     */
    cancel?: object;
    /**
     * Specifies which request types can be augmented with tasks.
     */
    requests?: {
      /**
       * Task support for tool-related requests.
       */
      tools?: {
        /**
         * Whether the server supports task-augmented tools/call requests.
         */
        call?: object;
      };
    };
  };
}
