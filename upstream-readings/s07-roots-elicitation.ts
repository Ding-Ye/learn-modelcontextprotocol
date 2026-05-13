/* Roots */
/**
 * Sent from the server to request a list of root URIs from the client. Roots allow
 * servers to ask for specific directories or files to operate on. A common example
 * for roots is providing a set of repositories or directories a server should operate
 * on.
 *
 * This request is typically used when the server needs to understand the file system
 * structure or access specific locations that the client has permission to read from.
 *
 * @category `roots/list`
 */
export interface ListRootsRequest extends JSONRPCRequest {
  method: "roots/list";
  params?: RequestParams;
}

/**
 * The client's response to a roots/list request from the server.
 * This result contains an array of Root objects, each representing a root directory
 * or file that the server can operate on.
 *
 * @category `roots/list`
 */
export interface ListRootsResult extends Result {
  roots: Root[];
}

/**
 * Represents a root directory or file that the server can operate on.
 *
 * @category `roots/list`
 */
export interface Root {
  /**
   * The URI identifying the root. This *must* start with file:// for now.
   * This restriction may be relaxed in future versions of the protocol to allow
   * other URI schemes.
   *
   * @format uri
   */
  uri: string;
  /**
   * An optional name for the root. This can be used to provide a human-readable
   * identifier for the root, which may be useful for display purposes or for
   * referencing the root in other parts of the application.
   */
  name?: string;

  /**
   * See [General fields: `_meta`](/specification/2025-11-25/basic/index#meta) for notes on `_meta` usage.
   */
  _meta?: { [key: string]: unknown };
}

/**
 * A notification from the client to the server, informing it that the list of roots has changed.
 * This notification should be sent whenever the client adds, removes, or modifies any root.
 * The server should then request an updated list of roots using the ListRootsRequest.
 *
 * @category `notifications/roots/list_changed`
 */
export interface RootsListChangedNotification extends JSONRPCNotification {
  method: "notifications/roots/list_changed";
  params?: NotificationParams;
}

/**
 * The parameters for a request to elicit non-sensitive information from the user via a form in the client.
 *
 * @category `elicitation/create`
 */
export interface ElicitRequestFormParams extends TaskAugmentedRequestParams {
  /**
   * The elicitation mode.
   */
  mode?: "form";

  /**
   * The message to present to the user describing what information is being requested.
   */
  message: string;

  /**
   * A restricted subset of JSON Schema.
   * Only top-level properties are allowed, without nesting.
   */
  requestedSchema: {
    $schema?: string;
    type: "object";
    properties: {
      [key: string]: PrimitiveSchemaDefinition;
    };
    required?: string[];
  };
}

/**
 * The parameters for a request to elicit information from the user via a URL in the client.
 *
 * @category `elicitation/create`
 */
export interface ElicitRequestURLParams extends TaskAugmentedRequestParams {
  /**
   * The elicitation mode.
   */
  mode: "url";

  /**
   * The message to present to the user explaining why the interaction is needed.
   */
  message: string;

  /**
   * The ID of the elicitation, which must be unique within the context of the server.
   * The client MUST treat this ID as an opaque value.
   */
  elicitationId: string;

  /**
   * The URL that the user should navigate to.
   *
   * @format uri
   */
  url: string;
}

/**
 * The parameters for a request to elicit additional information from the user via the client.
 *
 * @category `elicitation/create`
 */
export type ElicitRequestParams =
  | ElicitRequestFormParams
  | ElicitRequestURLParams;

/**
 * A request from the server to elicit additional information from the user via the client.
 *
 * @category `elicitation/create`
 */
export interface ElicitRequest extends JSONRPCRequest {
  method: "elicitation/create";
  params: ElicitRequestParams;
}

/**
 * Restricted schema definitions that only allow primitive types
 * without nested objects or arrays.
 *
 * @category `elicitation/create`
 */
export type PrimitiveSchemaDefinition =
  | StringSchema
  | NumberSchema
  | BooleanSchema
  | EnumSchema;

/**
 * @category `elicitation/create`
 */
export interface StringSchema {
  type: "string";
  title?: string;
  description?: string;
  minLength?: number;
  maxLength?: number;
  format?: "email" | "uri" | "date" | "date-time";
  default?: string;
}

/**
 * @category `elicitation/create`
 */
export interface NumberSchema {
  type: "number" | "integer";
  title?: string;
  description?: string;
  minimum?: number;
  maximum?: number;
  default?: number;
}

/**
 * @category `elicitation/create`
 */
export interface BooleanSchema {
  type: "boolean";
  title?: string;
  description?: string;
  default?: boolean;
}

/**
 * Schema for single-selection enumeration without display titles for options.
 *
 * @category `elicitation/create`
 */
export interface UntitledSingleSelectEnumSchema {
  type: "string";
  /**
   * Optional title for the enum field.
   */
  title?: string;
  /**
   * Optional description for the enum field.
   */
  description?: string;
  /**
   * Array of enum values to choose from.
   */
  enum: string[];
  /**
   * Optional default value.
   */
  default?: string;
}

/**
 * Schema for single-selection enumeration with display titles for each option.
 *
 * @category `elicitation/create`
 */
export interface TitledSingleSelectEnumSchema {
  type: "string";
  /**
   * Optional title for the enum field.
   */
  title?: string;
  /**
   * Optional description for the enum field.
   */
  description?: string;
  /**
   * Array of enum options with values and display labels.
   */
  oneOf: Array<{
    /**
     * The enum value.
     */
    const: string;
    /**
     * Display label for this option.
     */
    title: string;
  }>;
  /**
   * Optional default value.
   */
  default?: string;
}

/**
 * @category `elicitation/create`
 */
// Combined single selection enumeration
export type SingleSelectEnumSchema =
  | UntitledSingleSelectEnumSchema
  | TitledSingleSelectEnumSchema;

/**
 * Schema for multiple-selection enumeration without display titles for options.
 *
 * @category `elicitation/create`
 */
export interface UntitledMultiSelectEnumSchema {
  type: "array";
  /**
   * Optional title for the enum field.
   */
  title?: string;
  /**
   * Optional description for the enum field.
   */
  description?: string;
  /**
   * Minimum number of items to select.
   */
  minItems?: number;
  /**
   * Maximum number of items to select.
   */
  maxItems?: number;
  /**
   * Schema for the array items.
   */
  items: {
    type: "string";
    /**
     * Array of enum values to choose from.
     */
    enum: string[];
  };
  /**
   * Optional default value.
   */
  default?: string[];
}

/**
 * Schema for multiple-selection enumeration with display titles for each option.
 *
 * @category `elicitation/create`
 */
export interface TitledMultiSelectEnumSchema {
  type: "array";
  /**
   * Optional title for the enum field.
   */
  title?: string;
  /**
   * Optional description for the enum field.
   */
  description?: string;
  /**
   * Minimum number of items to select.
   */
  minItems?: number;
  /**
   * Maximum number of items to select.
   */
  maxItems?: number;
  /**
   * Schema for array items with enum options and display labels.
   */
  items: {
    /**
     * Array of enum options with values and display labels.
     */
    anyOf: Array<{
      /**
       * The constant enum value.
       */
      const: string;
      /**
       * Display title for this option.
       */
      title: string;
    }>;
  };
  /**
   * Optional default value.
   */
  default?: string[];
}

/**
 * @category `elicitation/create`
 */
// Combined multiple selection enumeration
export type MultiSelectEnumSchema =
  | UntitledMultiSelectEnumSchema
  | TitledMultiSelectEnumSchema;

/**
 * Use TitledSingleSelectEnumSchema instead.
 * This interface will be removed in a future version.
 *
 * @category `elicitation/create`
 */
export interface LegacyTitledEnumSchema {
  type: "string";
  title?: string;
  description?: string;
  enum: string[];
  /**
   * (Legacy) Display names for enum values.
   * Non-standard according to JSON schema 2020-12.
   */
  enumNames?: string[];
  default?: string;
}

/**
 * @category `elicitation/create`
 */
// Union type for all enum schemas
export type EnumSchema =
  | SingleSelectEnumSchema
  | MultiSelectEnumSchema
  | LegacyTitledEnumSchema;

/**
 * The client's response to an elicitation request.
 *
 * @category `elicitation/create`
 */
export interface ElicitResult extends Result {
  /**
   * The user action in response to the elicitation.
   * - "accept": User submitted the form/confirmed the action
   * - "decline": User explicitly decline the action
   * - "cancel": User dismissed without making an explicit choice
   */
  action: "accept" | "decline" | "cancel";

  /**
   * The submitted form data, only present when action is "accept" and mode was "form".
   * Contains values matching the requested schema.
   * Omitted for out-of-band mode responses.
   */
  content?: { [key: string]: string | number | boolean | string[] };
}

/**
 * An optional notification from the server to the client, informing it of a completion of a out-of-band elicitation request.
 *
 * @category `notifications/elicitation/complete`
 */
export interface ElicitationCompleteNotification extends JSONRPCNotification {
  method: "notifications/elicitation/complete";
  params: {
    /**
     * The ID of the elicitation that completed.
     */
    elicitationId: string;
  };
}
