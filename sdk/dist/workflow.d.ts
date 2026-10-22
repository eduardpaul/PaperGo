/**
 * -------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation.  All Rights Reserved.  Licensed under the MIT License.
 * See License in the project root for license information.
 * -------------------------------------------------------------------------------------------
 */
/** Defines a contract for models that can hold additional data besides the described properties. */
export interface AdditionalDataHolder {
	/**
	 * Gets the additional data for this object that did not belong to the properties.
	 * @returns The additional data for this object.
	 */
	additionalData?: Record<string, unknown>;
}
/**
 * -------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation.  All Rights Reserved.  Licensed under the MIT License.
 * See License in the project root for license information.
 * -------------------------------------------------------------------------------------------
 */
/**
 * Defines a serializable model object.
 */
export interface Parsable {
}
/**
 * -------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation.  All Rights Reserved.  Licensed under the MIT License.
 * See License in the project root for license information.
 * -------------------------------------------------------------------------------------------
 */
export type Guid = string;
declare class DateOnly implements DateOnlyInterface {
	/**
	 * Creates a new DateOnly from the given string.
	 * @param root0 The year, month, and day
	 * @param root0.year The year
	 * @param root0.month The month
	 * @param root0.day The day
	 * @returns The new DateOnly
	 * @throws An error if the year is invalid
	 * @throws An error if the month is invalid
	 * @throws An error if the day is invalid
	 */
	constructor({ year, month, day }: Partial<DateOnlyInterface>);
	year: number;
	month: number;
	day: number;
	/**
	 * Creates a new DateOnly from the given date.
	 * @param date The date
	 * @returns The new DateOnly
	 * @throws An error if the date is invalid
	 */
	static fromDate(date: Date): DateOnly;
	/**
	 * Parses a string into a DateOnly. The string can be of the ISO 8601 time only format or a number representing the ticks of a Date.
	 * @param value The value to parse
	 * @returns The parsed DateOnly.
	 * @throws An error if the value is invalid
	 */
	static parse(value: string | undefined): DateOnly | undefined;
	/**
	 *  Returns a string representation of the date in the format YYYY-MM-DD
	 * @returns The date in the format YYYY-MM-DD ISO 8601
	 */
	toString(): string;
}
export interface DateOnlyInterface {
	/**
	 * The year
	 * @default 0
	 */
	year: number;
	/**
	 * The month
	 * @default 1
	 */
	month: number;
	/**
	 * The day
	 * @default 1
	 */
	day: number;
}
declare class Duration implements DurationInterface {
	/**
	 * Creates a new Duration value from the given parameters.
	 * @param root0 The years, months, weeks, days, hours, minutes, seconds, and negative flag
	 * @param root0.years The years
	 * @param root0.months The months
	 * @param root0.weeks The weeks
	 * @param root0.days The days
	 * @param root0.hours The hours
	 * @param root0.minutes The minutes
	 * @param root0.seconds The seconds
	 * @param root0.negative The negative flag
	 * @returns The new Duration
	 * @throws An error if years is invalid
	 * @throws An error if months is invalid
	 * @throws An error if weeks is invalid
	 * @throws An error if days is invalid
	 * @throws An error if hours is invalid
	 * @throws An error if minutes is invalid
	 * @throws An error if seconds is invalid
	 * @throws An error if weeks is used in combination with years or months
	 */
	constructor({ years, months, weeks, days, hours, minutes, seconds, negative }: Partial<DurationInterface>);
	years: number;
	months: number;
	weeks: number;
	days: number;
	hours: number;
	minutes: number;
	seconds: number;
	negative: boolean;
	/**
	 * Parses a string into a Duration. The string can be of the ISO 8601 duration format.
	 * @param value The value to parse
	 * @returns The parsed Duration.
	 * @throws An error if the value is invalid
	 */
	static parse(value: string | undefined): Duration | undefined;
	/**
	 * Serializes the duration to a string in the ISO 8601 duration format.
	 * @returns The serialized duration.
	 */
	toString(): string;
}
export interface DurationInterface {
	/**
	 * Years of the duration
	 * @default 0
	 */
	years: number;
	/**
	 * Months of the duration
	 * @default 0
	 */
	months: number;
	/**
	 * Weeks of the duration, can't be used together with years or months
	 * @default 0
	 */
	weeks: number;
	/**
	 * Days of the duration
	 * @default 0
	 */
	days: number;
	/**
	 * Hours of the duration
	 * @default 0
	 */
	hours: number;
	/**
	 * Minutes of the duration
	 * @default 0
	 */
	minutes: number;
	/**
	 * Seconds of the duration
	 * @default 0
	 */
	seconds: number;
	/**
	 * Whether the duration is negative
	 * @default false
	 */
	negative: boolean;
}
declare class Headers extends Map<string, Set<string>> {
	private headers;
	private readonly singleValueHeaders;
	/**
	 * Creates a new Headers object.
	 * @param entries An iterable object that contains key-value pairs. Each key-value pair must be an array with two elements: the key of the header, and the value of the header.
	 * @example
	 * ```typescript
	 *  const entries: [string, Set<string>][] = [
	 *    ['header1', new Set(['value1'])],
	 *    ['header2', new Set(['value2', 'value3'])]
	 *  ];
	 *  const headers = new Headers(entries);
	 * ```
	 */
	constructor(entries?: readonly (readonly [
		string,
		Set<string>
	])[] | null);
	/**
	 * Sets a header with the specified name and value. If a header with the same name already exists, its value is appended with the specified value.
	 * @param headerName the name of the header to set
	 * @param headerValue the value of the header to set
	 * @returns Headers object
	 */
	set(headerName: string, headerValue: Set<string>): this;
	/**
	 * Gets the values for the header with the specified name.
	 * @param headerName The name of the header to get the values for.
	 * @returns The values for the header with the specified name.
	 * @throws Error if headerName is null or empty
	 */
	get(headerName: string): Set<string> | undefined;
	/**
	 * Checks if a header exists.
	 * @param key The name of the header to check for.
	 * @returns whether or not a header with the given name/key exists.
	 */
	has(key: string): boolean;
	/**
	 * Delete the header with the specified name.
	 * @param headerName The name of the header to delete.
	 * @returns Whether or not the header existed and was deleted.
	 * @throws Error if headerName is null or empty
	 */
	delete(headerName: string): boolean;
	/**
	 * clear the headers collection
	 */
	clear(): void;
	/**
	 * execute a provided function once per each header
	 * @param callbackfn A function that accepts up to three arguments. forEach calls the callbackfn function one time for each header in the dictionary.
	 * @param thisArg An object to which the this keyword can refer in the callbackfn function. If thisArg is omitted, undefined is used as the this value.
	 */
	forEach(callbackfn: (value: Set<string>, key: string, map: Map<string, Set<string>>) => void, thisArg?: any): void;
	/**
	 * Adds values to the header with the specified name.
	 * @param headerName The name of the header to add values to.
	 * @param headerValues The values to add to the header.
	 * @returns Whether or not the values were added to the header.
	 */
	add(headerName: string, ...headerValues: string[]): boolean;
	/**
	 * Adds values to the header with the specified name if it's not already present
	 * @param headerName The name of the header to add values to.
	 * @param headerValue The values to add to the header.
	 * @returns If the headerValue have been added to the Dictionary.
	 */
	tryAdd(headerName: string, headerValue: string): boolean;
	/**
	 * Removes the specified value from the header with the specified name.
	 * @param headerName The name of the header to remove the value from.
	 * @param headerValue The value to remove from the header.
	 * @returns Whether or not the header existed and was removed.
	 * @throws Error if headerName is null or empty
	 * @throws Error if headerValue is null
	 */
	remove(headerName: string, headerValue: string): boolean;
	/**
	 * Adds all the headers values from the specified headers collection.
	 * @param headers The headers to update the current headers with.
	 * @throws Error if headers is null
	 */
	addAll(headers: Headers): void;
	/**
	 * Adds all headers from the request configuration value to the current headers collection.
	 * Replaces any existing headers with the same key.
	 * @param headers The headers to update the current headers with.
	 * @throws Error if headers is null
	 */
	addAllRaw(headers: Record<string, string | string[]>): void;
	/**
	 * Gets the values for the header with the specified name.
	 * @param key The name of the header to get the values for.
	 * @returns The values for the header with the specified name.
	 * @throws Error if key is null or empty
	 */
	tryGetValue(key: string): string[] | null;
	/**
	 * Override toString method for the headers collection
	 * @returns a string representation of the headers collection
	 */
	toString(): string;
	/**
	 * check if the headers collection is empty
	 * @returns a boolean indicating if the headers collection is empty
	 */
	isEmpty(): boolean;
	/**
	 * get keys of the headers collection
	 * @returns an iterator of keys
	 */
	keys(): MapIterator<string>;
	/**
	 * get entries
	 * @returns an iterator of entries
	 */
	entries(): MapIterator<[
		string,
		Set<string>
	]>;
}
declare enum HttpMethod {
	/** The HTTP GET method */
	GET = "GET",
	/** The HTTP POST method */
	POST = "POST",
	/** The HTTP PATCH method */
	PATCH = "PATCH",
	/** The HTTP DELETE method */
	DELETE = "DELETE",
	/** The HTTP OPTIONS method */
	OPTIONS = "OPTIONS",
	/** The HTTP CONNECT method */
	CONNECT = "CONNECT",
	/** The HTTP TRACE method */
	TRACE = "TRACE",
	/** The HTTP HEAD method */
	HEAD = "HEAD",
	/** The HTTP PUT method */
	PUT = "PUT"
}
/**
 * -------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation.  All Rights Reserved.  Licensed under the MIT License.
 * See License in the project root for license information.
 * -------------------------------------------------------------------------------------------
 */
/** Represents a request option. */
export interface RequestOption {
	/** Gets the option key for when adding it to a request. Must be unique. */
	getKey(): string;
}
/**
 * Request configuration
 * @template T Query parameters type
 */
export interface RequestConfiguration<T extends object> {
	/**
	 * Request headers
	 */
	headers?: Record<string, string | string[]>;
	/**
	 * Request options
	 */
	options?: RequestOption[];
	/**
	 * Request query parameters
	 */
	queryParameters?: T;
}
declare class RequestInformation implements RequestInformationSetContent {
	/**
	 * Initializes a request information instance with the provided values.
	 * @param httpMethod The HTTP method for the request.
	 * @param urlTemplate The URL template for the request.
	 * @param pathParameters The path parameters for the request.
	 */
	constructor(httpMethod?: HttpMethod, urlTemplate?: string, pathParameters?: Record<string, unknown>);
	/** The URI of the request. */
	private uri?;
	/** The path parameters for the request. */
	pathParameters: Record<string, unknown>;
	/** The URL template for the request */
	urlTemplate?: string;
	/**
	 * Gets the URL of the request
	 * @returns the url string
	 */
	get URL(): string;
	/** Sets the URL of the request */
	set URL(url: string);
	static readonly raw_url_key = "request-raw-url";
	/** The HTTP method for the request */
	httpMethod?: HttpMethod;
	/** The Request Body. */
	content?: ArrayBuffer;
	/** The Query Parameters of the request. */
	queryParameters: Record<string, string | number | boolean | string[] | number[] | undefined>;
	/** The Request Headers. */
	headers: Headers;
	private _requestOptions;
	/**
	 * Gets the request options for the request.
	 * @returns the request options.
	 */
	getRequestOptions(): Record<string, RequestOption>;
	/**
	 * Adds the headers for the request.
	 * @param source The source collection to add the headers to
	 */
	addRequestHeaders(source: Record<string, string | string[]> | undefined): void;
	/**
	 * Adds the request options for the request.
	 * @param options the options to add.
	 */
	addRequestOptions(options: RequestOption[] | undefined): void;
	/**
	 * Removes the request options for the request.
	 * @param options the options to remove.
	 */
	removeRequestOptions(...options: RequestOption[]): void;
	private static readonly binaryContentType;
	private static readonly contentTypeHeader;
	private static readonly tracerKey;
	private static readonly requestTypeKey;
	/**
	 * Sets the request body from a model with the specified content type.
	 * @param requestAdapter The adapter service to get the serialization writer from.
	 * @param contentType the content type.
	 * @param value the models.
	 * @param modelSerializerFunction the serialization function for the model type.
	 */
	setContentFromParsable: <T extends Parsable>(requestAdapter?: RequestAdapter, contentType?: string, value?: T[] | T, modelSerializerFunction?: ModelSerializerFunction<T>) => void;
	private readonly setContentAndContentType;
	private readonly getSerializationWriter;
	/**
	 * Sets the request body from a model with the specified content type.
	 * @param requestAdapter The adapter service to get the serialization writer from.
	 * @param contentType the content type.
	 * @param value the scalar values to serialize.
	 */
	setContentFromScalar: <T extends PrimitiveTypesForDeserializationType>(requestAdapter: RequestAdapter | undefined, contentType: string | undefined, value: T[] | T) => void;
	/**
	 * Sets the request body to be a binary stream.
	 * @param value the binary stream
	 * @param contentType the content type.
	 */
	setStreamContent: (value: ArrayBuffer, contentType?: string) => void;
	private normalizeValue;
	/**
	 * Sets the query string parameters from a raw object.
	 * @param q parameters the parameters.
	 * @param p the mapping from code symbol to URI template parameter name.
	 */
	setQueryStringParametersFromRawObject<T extends object>(q?: T, p?: Record<string, string>): void;
	/**
	 * Configure the current request with headers, query parameters and options.
	 * @param config the configuration object to use.
	 * @param queryParametersMapper mapping between code symbols and URI template parameter names.
	 */
	configure<T extends object>(config?: RequestConfiguration<T>, queryParametersMapper?: Record<string, string>): void;
}
/**
 * Describes the contract of request adapter set content methods so it can be used in request metadata.
 */
export interface RequestInformationSetContent {
	setStreamContent(value: ArrayBuffer, contentType?: string): void;
	setContentFromScalar<T extends PrimitiveTypesForDeserializationType>(requestAdapter: RequestAdapter | undefined, contentType: string | undefined, value: T[] | T): void;
	setContentFromParsable<T extends Parsable>(requestAdapter?: RequestAdapter, contentType?: string, value?: T[] | T, modelSerializerFunction?: ModelSerializerFunction<T>): void;
}
/**
 * -------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation.  All Rights Reserved.  Licensed under the MIT License.
 * See License in the project root for license information.
 * -------------------------------------------------------------------------------------------
 */
/**
 * Stores model information in a different location than the object properties. Implementations can provide dirty tracking capabilities, caching capabilities or integration with 3rd party stores.
 */
export interface BackingStore {
	/**
	 * Gets a value from the backing store based on its key. Returns null if the value hasn't changed and "ReturnOnlyChangedValues" is true.
	 * @returns The value from the backing store.
	 * @param key The key to lookup the backing store with.
	 */
	get<T>(key: string): T | undefined;
	/**
	 * Sets or updates the stored value for the given key.
	 * Will trigger subscriptions callbacks.
	 * @param key The key to store and retrieve the information.
	 * @param value The value to be stored.
	 */
	set<T>(key: string, value: T): void;
	/**
	 * Enumerates all the values stored in the backing store. Values will be filtered if "ReturnOnlyChangedValues" is true.
	 * @returns The values available in the backing store.
	 */
	enumerate(): {
		key: string;
		value: unknown;
	}[];
	/**
	 * Enumerates the keys for all values that changed to null.
	 * @returns The keys for the values that changed to null.
	 */
	enumerateKeysForValuesChangedToNull(): string[];
	/**
	 * Creates a subscription to any data change happening.
	 * @param callback Callback to be invoked on data changes where the first parameter is the data key, the second the previous value and the third the new value.
	 * @param subscriptionId The subscription Id to use.
	 * @returns The subscription Id to use when removing the subscription
	 */
	subscribe(callback: () => {
		key: string;
		previousValue: unknown;
		newValue: unknown;
	}, subscriptionId?: string): string;
	/**
	 * Removes a subscription from the store based on its subscription id.
	 * @param subscriptionId The Id of the subscription to remove.
	 */
	unsubscribe(subscriptionId: string): void;
	/**
	 * Clears the data stored in the backing store. Doesn't trigger any subscription.
	 */
	clear(): void;
	/**
	 * Whether the initialization of the object and/or the initial deserialization has been completed to track whether objects have changed.
	 */
	initializationCompleted: boolean;
	/**
	 * Whether to return only values that have changed since the initialization of the object when calling the Get and Enumerate methods.
	 */
	returnOnlyChangedValues: boolean;
}
/** Defines the contract for a factory that creates backing stores. */
export interface BackingStoreFactory {
	/**
	 * Creates a new instance of the backing store.
	 * @returns a new instance of the backing store.
	 */
	createBackingStore(): BackingStore;
}
declare class TimeOnly implements TimeOnlyInterface {
	/**
	 * Creates a new TimeOnly from the given parameters.
	 * @param root0 The hours, minutes, seconds, and milliseconds
	 * @param root0.hours The hours
	 * @param root0.minutes The minutes
	 * @param root0.seconds The seconds
	 * @param root0.picoseconds The milliseconds
	 * @returns The new TimeOnly
	 * @throws An error if the milliseconds are invalid
	 * @throws An error if the seconds are invalid
	 * @throws An error if the minutes are invalid
	 * @throws An error if the hours are invalid
	 * @throws An error if the milliseconds are invalid
	 */
	constructor({ hours, minutes, seconds, picoseconds }: Partial<TimeOnlyInterface>);
	hours: number;
	minutes: number;
	seconds: number;
	picoseconds: number;
	/**
	 * Creates a new TimeOnly from the given date.
	 * @param date The date
	 * @returns The new TimeOnly
	 * @throws An error if the date is invalid
	 */
	static fromDate(date: Date): TimeOnly;
	/**
	 * Parses a string into a TimeOnly. The string can be of the ISO 8601 time only format or a number representing the ticks of a Date.
	 * @param value The value to parse
	 * @returns The parsed TimeOnly.
	 * @throws An error if the value is invalid
	 */
	static parse(value: string | undefined): TimeOnly | undefined;
	/**
	 * Returns a string representation of the time in the format HH:MM:SS.SSSSSSS
	 * @returns The time in the format HH:MM:SS.SSSSSSS
	 * @throws An error if the time is invalid
	 */
	toString(): string;
}
export interface TimeOnlyInterface {
	/**
	 * The hours
	 * @default 0
	 */
	hours: number;
	/**
	 * The minutes
	 * @default 0
	 */
	minutes: number;
	/**
	 * The seconds
	 * @default 0
	 */
	seconds: number;
	/**
	 * The milliseconds
	 * @default 0
	 */
	picoseconds: number;
}
/** Service responsible for translating abstract Request Info into concrete native HTTP requests. */
export interface RequestAdapter {
	/**
	 * Gets the serialization writer factory currently in use for the HTTP core service.
	 * @returns the serialization writer factory currently in use for the HTTP core service.
	 */
	getSerializationWriterFactory(): SerializationWriterFactory;
	/**
	 * Gets the parse node factory currently in use for the HTTP core service.
	 * @returns the parse node factory currently in use for the HTTP core service.
	 */
	getParseNodeFactory(): ParseNodeFactory;
	/**
	 * Gets the backing store factory currently in use for the HTTP core service.
	 * @returns The backing store factory currently in use for the HTTP core service.
	 */
	getBackingStoreFactory(): BackingStoreFactory;
	/**
	 * Executes the HTTP request specified by the given RequestInformation and returns the deserialized response model.
	 * @param requestInfo the request info to execute.
	 * @param type the class of the response model to deserialize the response into.
	 * @param errorMappings the error factories mapping to use in case of a failed request.
	 * @returns a {@link Promise} with the deserialized response model.
	 */
	send<ModelType extends Parsable>(requestInfo: RequestInformation, type: ParsableFactory<ModelType>, errorMappings: ErrorMappings | undefined): Promise<ModelType | undefined>;
	/**
	 * Executes the HTTP request specified by the given RequestInformation and returns the deserialized response model collection.
	 * @param requestInfo the request info to execute.
	 * @param type the class of the response model to deserialize the response into.
	 * @param errorMappings the error factories mapping to use in case of a failed request.
	 * @returns a {@link Promise} with the deserialized response model collection.
	 */
	sendCollection<ModelType extends Parsable>(requestInfo: RequestInformation, type: ParsableFactory<ModelType>, errorMappings: ErrorMappings | undefined): Promise<ModelType[] | undefined>;
	/**
	 * Executes the HTTP request specified by the given RequestInformation and returns the deserialized response model collection.
	 * @param requestInfo the request info to execute.
	 * @param responseType the class of the response model to deserialize the response into.
	 * @param errorMappings the error factories mapping to use in case of a failed request.
	 * @returns a {@link Promise} with the deserialized response model collection.
	 */
	sendCollectionOfPrimitive<ResponseType extends PrimitiveTypesForDeserializationTypeForCollection>(requestInfo: RequestInformation, responseType: PrimitiveTypesForDeserializationForCollection, errorMappings: ErrorMappings | undefined): Promise<ResponseType[] | undefined>;
	/**
	 * Executes the HTTP request specified by the given RequestInformation and returns the deserialized primitive response model.
	 * @param requestInfo the request info to execute.
	 * @param responseType the class of the response model to deserialize the response into.
	 * @param errorMappings the error factories mapping to use in case of a failed request.
	 * @returns a {@link Promise} with the deserialized primitive response model.
	 */
	sendPrimitive<ResponseType extends PrimitiveTypesForDeserializationType>(requestInfo: RequestInformation, responseType: PrimitiveTypesForDeserialization, errorMappings: ErrorMappings | undefined): Promise<ResponseType | undefined>;
	/**
	 * Executes the HTTP request specified by the given RequestInformation and returns the deserialized primitive response model.
	 * @param requestInfo the request info to execute.
	 * @param errorMappings the error factories mapping to use in case of a failed request.
	 * @returns a {@link Promise} of void.
	 */
	sendNoResponseContent(requestInfo: RequestInformation, errorMappings: ErrorMappings | undefined): Promise<void>;
	/**
	 * Executes the HTTP request specified by the given RequestInformation and returns the deserialized enum response model.
	 * @template EnumObject - The type of the enum object. Must extend Record<string, unknown>.
	 * @param requestInfo - The request info to execute.
	 * @param enumObject - The Enum object expected in the response.
	 * @param errorMappings - the error factories mapping to use in case of a failed request.
	 * @returns A promise that resolves to the response of the request, or undefined if an error occurred.
	 */
	sendEnum<EnumObject extends Record<string, unknown>>(requestInfo: RequestInformation, enumObject: EnumObject, errorMappings: ErrorMappings | undefined): Promise<EnumObject[keyof EnumObject] | undefined>;
	/**
	 * Executes the HTTP request specified by the given RequestInformation and returns the deserialized response model collection.
	 * @template EnumObject - The type of the enum objects. Must extend Record<string, unknown>.
	 * @param requestInfo - The request info to execute.
	 * @param enumObject - The Enum object expected in the response.
	 * @param errorMappings - the error factories mapping to use in case of a failed request.
	 * @returns a promise with the deserialized response model collection.
	 */
	sendCollectionOfEnum<EnumObject extends Record<string, unknown>>(requestInfo: RequestInformation, enumObject: EnumObject, errorMappings: ErrorMappings | undefined): Promise<EnumObject[keyof EnumObject][] | undefined>;
	/**
	 * Enables the backing store proxies for the SerializationWriters and ParseNodes in use.
	 * @param backingStoreFactory the backing store factory to use.
	 */
	enableBackingStore(backingStoreFactory?: BackingStoreFactory): void;
	/** The base url for every request. */
	baseUrl: string;
	/**
	 * Converts the given RequestInformation into a native HTTP request used by the implementing adapter.
	 * @param requestInfo the request info to convert.
	 * @returns a {@link Promise} with the native request.
	 */
	convertToNativeRequest<T>(requestInfo: RequestInformation): Promise<T>;
}
export interface ErrorMappings {
	_4XX?: ParsableFactory<Parsable>;
	_5XX?: ParsableFactory<Parsable>;
	XXX?: ParsableFactory<Parsable>;
	[key: number]: ParsableFactory<Parsable>;
}
export type PrimitiveTypesForDeserializationType = string | number | boolean | Date | DateOnly | TimeOnly | Duration | ArrayBuffer;
export type PrimitiveTypesForDeserialization = "string" | "number" | "boolean" | "Date" | "DateOnly" | "TimeOnly" | "Duration" | "ArrayBuffer";
/** Primitive value types supported for collection response deserialization. */
export type PrimitiveTypesForDeserializationTypeForCollection = Exclude<PrimitiveTypesForDeserializationType, ArrayBuffer>;
/** Runtime primitive type names supported for collection response deserialization. */
export type PrimitiveTypesForDeserializationForCollection = Exclude<PrimitiveTypesForDeserialization, "ArrayBuffer">;
/**
 * Interface for a deserialization node in a parse tree. This interface provides an abstraction layer over serialization formats, libraries and implementations.
 */
export interface ParseNode {
	/**
	 * Gets the string value of the node.
	 * @returns the string value of the node.
	 */
	getStringValue(): string | undefined;
	/**
	 * Gets a new parse node for the given identifier.
	 * @param identifier the identifier of the current node property.
	 * @returns a new parse node for the given identifier.
	 */
	getChildNode(identifier: string): ParseNode | undefined;
	/**
	 * Gets the boolean value of the node.
	 * @returns the boolean value of the node.
	 */
	getBooleanValue(): boolean | undefined;
	/**
	 * Gets the Number value of the node.
	 * @returns the Number value of the node.
	 */
	getNumberValue(): number | undefined;
	/**
	 * Gets the Guid value of the node.
	 * @returns the Guid value of the node.
	 */
	getGuidValue(): Guid | undefined;
	/**
	 * Gets the Date value of the node.
	 * @returns the Date value of the node.
	 */
	getDateValue(): Date | undefined;
	/**
	 * Gets the Duration value of the node.
	 * @returns the Duration value of the node.
	 */
	getDurationValue(): Duration | undefined;
	/**
	 * Gets the DateOnly value of the node.
	 * @returns the DateOnly value of the node.
	 */
	getDateOnlyValue(): DateOnly | undefined;
	/**
	 * Gets the TimeOnly value of the node.
	 * @returns the TimeOnly value of the node.
	 */
	getTimeOnlyValue(): TimeOnly | undefined;
	/**
	 * Gets the collection of primitive values of the node.
	 * @param primitiveType the runtime primitive type to deserialize collection items into.
	 * @returns the collection of primitive values of the node.
	 */
	getCollectionOfPrimitiveValues<T>(primitiveType: PrimitiveTypesForDeserializationForCollection): T[] | undefined;
	/**
	 * Gets the collection of object values of the node.
	 * @returns the collection of object values of the node.
	 */
	getCollectionOfObjectValues<T extends Parsable>(parsableFactory: ParsableFactory<T>): T[] | undefined;
	/**
	 * Gets the model object value of the node.
	 * @returns the model object value of the node.
	 */
	getObjectValue<T extends Parsable>(parsableFactory: ParsableFactory<T>): T;
	/**
	 * Gets the Enum values of the node.
	 * @returns the Enum values of the node.
	 */
	getCollectionOfEnumValues<T>(type: any): T[];
	/**
	 * Gets the Enum value of the node.
	 * @returns the Enum value of the node.
	 */
	getEnumValue<T>(type: any): T | undefined;
	/**
	 * Gets the callback called before the node is deserialized.
	 * @returns the callback called before the node is deserialized.
	 */
	onBeforeAssignFieldValues: ((value: Parsable) => void) | undefined;
	/**
	 * Gets the callback called after the node is deserialized.
	 * @returns the callback called after the node is deserialized.
	 */
	onAfterAssignFieldValues: ((value: Parsable) => void) | undefined;
	/**
	 * Gets the byte array value of the node.
	 * @returns the byte array value of the node.
	 */
	getByteArrayValue(): ArrayBuffer | undefined;
}
/** Defines an interface for serialization of objects to a stream. */
export interface SerializationWriter {
	/**
	 * Writes the specified byte array value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeByteArrayValue(key?: string, value?: ArrayBuffer | null): void;
	/**
	 * Writes the specified string value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeStringValue(key?: string, value?: string | null): void;
	/**
	 * Writes the specified boolean value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeBooleanValue(key?: string, value?: boolean | null): void;
	/**
	 * Writes the specified number value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeNumberValue(key?: string, value?: number | null): void;
	/**
	 * Writes the specified Guid value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeGuidValue(key?: string, value?: Guid | null): void;
	/**
	 * Writes the specified Date value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeDateValue(key?: string, value?: Date | null): void;
	/**
	 * Writes the specified Duration value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeDurationValue(key?: string, value?: Duration | null): void;
	/**
	 * Writes the specified TimeOnly value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeTimeOnlyValue(key?: string, value?: TimeOnly | null): void;
	/**
	 * Writes the specified DateOnly value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeDateOnlyValue(key?: string, value?: DateOnly | null): void;
	/**
	 * Writes the specified collection of primitive values to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param values the value to write to the stream.
	 */
	writeCollectionOfPrimitiveValues<T>(key?: string, values?: T[] | null): void;
	/**
	 * Writes the specified collection of object values to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param values the value to write to the stream.
	 */
	writeCollectionOfObjectValues<T extends Parsable>(key?: string, values?: T[] | null, serializerMethod?: ModelSerializerFunction<T>): void;
	/**
	 * Writes the specified model object value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param value the value to write to the stream.
	 */
	writeObjectValue<T extends Parsable>(key?: string, value?: T | null, serializerMethod?: ModelSerializerFunction<T>): void;
	/**
	 * Writes the specified enum value to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param values the value to write to the stream.
	 */
	writeEnumValue<T>(key?: string, ...values: (T | null | undefined)[]): void;
	/**
	 * Writes the specified collection of enum values to the stream with an optional given key.
	 * @param key the key to write the value with.
	 * @param values the value to write to the stream.
	 */
	writeCollectionOfEnumValues<T>(key?: string, values?: (T | null | undefined)[]): void;
	/**
	 * Writes a null value for the specified key.
	 * @param key the key to write the value with.
	 */
	writeNullValue(key?: string): void;
	/**
	 * Gets the value of the serialized content.
	 * @returns the value of the serialized content.
	 */
	getSerializedContent(): ArrayBuffer;
	/**
	 * Writes the specified additional data values to the stream with an optional given key.
	 * @param value the values to write to the stream.
	 */
	writeAdditionalData(value: Record<string, unknown> | undefined): void;
	/**
	 * Gets the callback called before the object gets serialized.
	 * @returns the callback called before the object gets serialized.
	 */
	onBeforeObjectSerialization: ((value: Parsable) => void) | undefined;
	/**
	 * Gets the callback called after the object gets serialized.
	 * @returns the callback called after the object gets serialized.
	 */
	onAfterObjectSerialization: ((value: Parsable) => void) | undefined;
	/**
	 * Gets the callback called right after the serialization process starts.
	 * @returns the callback called right after the serialization process starts.
	 */
	onStartObjectSerialization: ((value: Parsable, writer: SerializationWriter) => void) | undefined;
}
export type ModelSerializerFunction<T extends Parsable> = (writer: SerializationWriter, value?: Partial<T> | null) => void;
export type DeserializeIntoModelFunction<T extends Parsable> = (value?: Partial<T>) => Record<string, (node: ParseNode) => void>;
/**
 * Defines the factory to get the deserializers constructing the parsable models.
 * @param parseNode The node to parse use to get the discriminator value from the payload.
 * @returns The parsable object.
 */
export type ParsableFactory<T extends Parsable> = (parseNode: ParseNode | undefined) => DeserializeIntoModelFunction<T>;
/**
 * Defines the contract for a factory that is used to create {@link ParseNode}s.
 */
export interface ParseNodeFactory {
	/**
	 * Returns the content type this factory's parse nodes can deserialize.
	 */
	getValidContentType(): string;
	/**
	 * Creates a {@link ParseNode} from the given {@link ArrayBuffer} and content type.
	 * @param contentType the content type of the {@link ArrayBuffer}.
	 * @param content the {@link ArrayBuffer} to read from.
	 * @returns a {@link ParseNode} that can deserialize the given {@link ArrayBuffer}.
	 */
	getRootParseNode(contentType: string, content: ArrayBuffer): ParseNode;
}
/** Defines the contract for a factory that creates SerializationWriter instances. */
export interface SerializationWriterFactory {
	/**
	 * Gets the content type this factory creates serialization writers for.
	 * @returns the content type this factory creates serialization writers for.
	 */
	getValidContentType(): string;
	/**
	 * Creates a new SerializationWriter instance for the given content type.
	 * @param contentType the content type to create a serialization writer for.
	 * @returns a new SerializationWriter instance for the given content type.
	 */
	getSerializationWriter(contentType: string): SerializationWriter;
}
/** Defines the base interface for defining an untyped node. */
export interface UntypedNode extends Parsable {
	/**
	 * Gets the value of the UntypedNode.
	 */
	getValue(): unknown;
	/**
	 * The value represented by the UntypedNode.
	 */
	value?: unknown;
}
/**
 * -------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation.  All Rights Reserved.  Licensed under the MIT License.
 * See License in the project root for license information.
 * -------------------------------------------------------------------------------------------
 */
/** Parent interface for errors thrown by the client when receiving failed responses to its requests. */
export interface ApiError extends Error {
	/** The status code for the error. */
	responseStatusCode: number | undefined;
	/** The Response Headers. */
	responseHeaders: Record<string, string[]> | undefined;
}
/**
 * -------------------------------------------------------------------------------------------
 * Copyright (c) Microsoft Corporation.  All Rights Reserved.  Licensed under the MIT License.
 * See License in the project root for license information.
 * -------------------------------------------------------------------------------------------
 */
export interface BaseRequestBuilder<T> {
	withUrl(rawUrl: string): T;
}
declare class MultipartBody implements Parsable {
	private readonly _boundary;
	private readonly _parts;
	requestAdapter?: RequestAdapter;
	/**
	 * Instantiates a new MultipartBody.
	 */
	constructor();
	/**
	 * Adds or replaces a part with the given name, content type and content.
	 * @param partName the name of the part to add or replace.
	 * @param partContentType the content type of the part to add or replace.
	 * @param content the content of the part to add or replace.
	 * @param serializationCallback the serialization callback to use when serializing the part.
	 * @param fileName the name of the file associated with this part.
	 */
	addOrReplacePart<T>(partName: string, partContentType: string, content: T, serializationCallback?: ModelSerializerFunction<Parsable>, fileName?: string): void;
	/**
	 * Gets the content of the part with the given name.
	 * @param partName the name of the part to get the content for.
	 * @returns the content of the part with the given name.
	 */
	getPartValue<T>(partName: string): T | undefined;
	/**
	 * Removes the part with the given name.
	 * @param partName the name of the part to remove.
	 * @returns true if the part was removed, false if it did not exist.
	 */
	removePart(partName: string): boolean;
	/**
	 * Gets the boundary used to separate each part.
	 * @returns the boundary value.
	 */
	getBoundary(): string;
	private normalizePartName;
	/**
	 * Lists all the parts in the multipart body.
	 * WARNING: meant for internal use only
	 * @returns the list of parts in the multipart body.
	 */
	listParts(): Record<string, MultipartEntry>;
}
export interface MultipartEntry {
	contentType: string;
	content: any;
	originalName: string;
	fileName?: string;
	serializationCallback?: ModelSerializerFunction<Parsable>;
}
/**
 * Apply compatible shared catalog field definitions and merge rules by key into the selected content type (default when omitted), recording one frozen collection schema. Other type memberships are retained.
 */
export interface ApplyTemplateInput extends Parsable {
	/**
	 * Collection content type ID. Omission selects the collection default.
	 */
	contentTypeId?: Guid | null;
	/**
	 * The template_version property
	 */
	templateVersion?: number | null;
}
export interface ArchiveEntry extends AdditionalDataHolder, Parsable {
	/**
	 * Lists and libraries only: permits creating physical folders. Disabling retains existing folders. Changes require manage access.
	 */
	allowFolders?: boolean | null;
	/**
	 * The archived property
	 */
	archived?: boolean | null;
	/**
	 * The archived_at property
	 */
	archivedAt?: Date | null;
	/**
	 * The archived_by property
	 */
	archivedBy?: string | null;
	/**
	 * The can_write property
	 */
	canWrite?: boolean | null;
	/**
	 * The container_id property
	 */
	containerId?: Guid | null;
	/**
	 * Items and folders: an attached collection content type. Creation selects the default when omitted. PATCH changes type, keeps shared fields and removes fields absent from the destination type. Folder required fields and creation defaults do not apply.
	 */
	contentTypeId?: Guid | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * Creation actor or selected content revision modification actor.
	 */
	createdBy?: string | null;
	/**
	 * Workspace only. Empty inherits the organization default. Changing this affects newly created currency fields, not existing definitions.
	 */
	defaultCurrency?: string | null;
	/**
	 * Workspace or collection description, limited to 2000 UTF-16 code units. Changes require manage access.
	 */
	description?: string | null;
	/**
	 * Libraries only. Default warn. Compares hashes against current documents visible to the caller across libraries and workspaces. Allow suppresses warnings; block rejects duplicates with 409 duplicate_file.
	 */
	duplicatePolicy?: ArchiveEntry_duplicate_policy | null;
	/**
	 * Omitted from published read surfaces.
	 */
	headRevisionId?: Guid | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The inherit_permissions property
	 */
	inheritPermissions?: boolean | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * The kind property
	 */
	kind?: ArchiveEntry_kind | null;
	/**
	 * History entries kept per item, trimmed on each new recorded version. Defaults to 50.
	 */
	maxVersions?: number | null;
	/**
	 * Item and folder titles contain at most 1024 UTF-16 units before trimming. Workspace and collection names allow 200 units. Library names also exclude path separators and control characters and are unique among live siblings ignoring case.
	 */
	name?: string | null;
	/**
	 * Internal content counter; omitted from published read surfaces.
	 */
	nextRevisionNumber?: number | null;
	/**
	 * The parent_id property
	 */
	parentId?: Guid | null;
	/**
	 * Owner of a personal Home workspace; membership is fixed.
	 */
	personalOwnerId?: string | null;
	/**
	 * The published_revision_id property
	 */
	publishedRevisionId?: Guid | null;
	/**
	 * Lists/libraries only. False publishes content changes immediately. Changing policy requires manage access; disabling publishes all current heads atomically.
	 */
	publishingEnabled?: boolean | null;
	/**
	 * The schema_head_id property
	 */
	schemaHeadId?: Guid | null;
	/**
	 * Protected default Home library identity.
	 */
	systemKey?: ArchiveEntry_system_key | null;
	/**
	 * Template keys adopted into a collection; used by smart folder selectors.
	 */
	templateKeys?: string[] | null;
	/**
	 * For items, creation timestamp of the selected content revision.
	 */
	updatedAt?: Date | null;
	/**
	 * Creation actor or selected content revision modification actor.
	 */
	updatedBy?: string | null;
	/**
	 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
	 */
	values?: ArchiveEntry_values | null;
	/**
	 * Optimistic resource lock version, independent of content revision_number and blob version.
	 */
	version?: number | null;
	/**
	 * Collections only. Lists and libraries default to off. Off stops recording new history; retained entries remain available. Replacing a file while off queues its previous bytes for reclamation after the one-hour grace period, subject to current/published file, staged-use and workflow-lease protection.
	 */
	versioning?: ArchiveEntry_versioning | null;
	/**
	 * Libraries only. Serves the library's folders and files at /webdav/{id}/.
	 */
	webdavEnabled?: boolean | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
export type ArchiveEntry_duplicate_policy = (typeof ArchiveEntry_duplicate_policyObject)[keyof typeof ArchiveEntry_duplicate_policyObject];
export type ArchiveEntry_kind = (typeof ArchiveEntry_kindObject)[keyof typeof ArchiveEntry_kindObject];
export type ArchiveEntry_system_key = (typeof ArchiveEntry_system_keyObject)[keyof typeof ArchiveEntry_system_keyObject];
/**
 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
 */
export interface ArchiveEntry_values extends AdditionalDataHolder, Parsable {
}
export type ArchiveEntry_versioning = (typeof ArchiveEntry_versioningObject)[keyof typeof ArchiveEntry_versioningObject];
export type ArchiveSelection = (typeof ArchiveSelectionObject)[keyof typeof ArchiveSelectionObject];
/**
 * A file an operation made from file versions for an item, kept for reuse and staging until expires_at, and longer while a staged file holds it.
 */
export interface Artifact extends Parsable {
	/**
	 * The content_type property
	 */
	contentType?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The expires_at property
	 */
	expiresAt?: Date | null;
	/**
	 * The filename property
	 */
	filename?: string | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The metrics property
	 */
	metrics?: Artifact_metrics | null;
	/**
	 * The operation that made it.
	 */
	operationId?: string | null;
	/**
	 * The sha256 property
	 */
	sha256?: string | null;
	/**
	 * The size property
	 */
	size?: number | null;
	/**
	 * The sources property
	 */
	sources?: Artifact_sources[] | null;
}
export interface Artifact_metrics extends AdditionalDataHolder, Parsable {
}
export interface Artifact_sources extends AdditionalDataHolder, Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
}
/**
 * Attach the organization definition by ID without copying. At most 32 types per collection. Selecting a default affects only this collection. Conflicting field keys are rejected; types that share a key must reference the same catalog field ID.
 */
export interface AttachContentTypeInput extends Parsable {
	/**
	 * The content_type_id property
	 */
	contentTypeId?: Guid | null;
	/**
	 * The is_default property
	 */
	isDefault?: boolean | null;
}
export interface AuditEvent extends AdditionalDataHolder, Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The details property
	 */
	details?: AuditEvent_details | null;
	/**
	 * The entity_id property
	 */
	entityId?: string | null;
	/**
	 * The entity_type property
	 */
	entityType?: string | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The properties property
	 */
	properties?: string[] | null;
	/**
	 * The resource_id property
	 */
	resourceId?: string | null;
	/**
	 * The subject property
	 */
	subject?: string | null;
	/**
	 * The trace_id property
	 */
	traceId?: string | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: string | null;
}
export interface AuditEvent_details extends AdditionalDataHolder, Parsable {
}
export interface AuditPage extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: AuditEvent[] | null;
}
export interface Blob extends AdditionalDataHolder, Parsable {
	/**
	 * The content_type property
	 */
	contentType?: Blob_content_type | null;
	/**
	 * Upload warnings: at most 20 current matching documents visible to the caller. Empty for allow and successful block uploads. Restoration does not apply upload duplicate policy.
	 */
	duplicates?: DuplicateDocument[] | null;
	/**
	 * Filename limited to 1024 UTF-16 units, without path separators or control characters.
	 */
	filename?: string | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * Per-upload OCR language hint; empty uses processing defaults. Restoration resets the hint.
	 */
	languages?: string | null;
	/**
	 * Resource lock version after upload; also returned as ETag.
	 */
	resourceVersion?: number | null;
	/**
	 * The sha256 property
	 */
	sha256?: string | null;
	/**
	 * The size property
	 */
	size?: number | null;
	/**
	 * Immutable blob sequence number, independent of the resource lock version and content revision number.
	 */
	version?: number | null;
}
export type Blob_content_type = (typeof Blob_content_typeObject)[keyof typeof Blob_content_typeObject];
export interface BulkCreate extends Parsable {
	/**
	 * Collection content type ID. Omission selects the collection default.
	 */
	contentTypeId?: Guid | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * Item or folder title. Limited to 1024 UTF-16 units before trimming.
	 */
	name?: string | null;
	/**
	 * The values property
	 */
	values?: BulkCreate_values | null;
}
export interface BulkCreate_values extends AdditionalDataHolder, Parsable {
}
export type BulkOperation = BulkOperationMember1 | BulkOperationMember2 | BulkOperationMember3 | BulkOperationMember4 | BulkOperationMember5 | BulkOperationMember6 | BulkOperationMember7;
export interface BulkOperationMember1 extends Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * The create property
	 */
	create?: BulkCreate | null;
	/**
	 * Existing folder in this collection. Defaults to the collection.
	 */
	parentId?: string | null;
}
export interface BulkOperationMember2 extends Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * Item in this collection. Each existing item may occur only once.
	 */
	id?: string | null;
	/**
	 * The update property
	 */
	update?: BulkUpdate | null;
	/**
	 * Current resource lock version; stale versions roll back the entire batch.
	 */
	version?: number | null;
}
export interface BulkOperationMember3 extends Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * Item in this collection. Each existing item may occur only once.
	 */
	id?: string | null;
	/**
	 * Current resource lock version; stale versions roll back the entire batch.
	 */
	version?: number | null;
}
export interface BulkOperationMember4 extends Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * Item in this collection. Each existing item may occur only once.
	 */
	id?: string | null;
	/**
	 * Current resource lock version; stale versions roll back the entire batch.
	 */
	version?: number | null;
}
export interface BulkOperationMember5 extends Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * Item in this collection. Each existing item may occur only once.
	 */
	id?: string | null;
	/**
	 * Current resource lock version; stale versions roll back the entire batch.
	 */
	version?: number | null;
}
export interface BulkOperationMember6 extends Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * Item in this collection. Each existing item may occur only once.
	 */
	id?: string | null;
	/**
	 * Current resource lock version; stale versions roll back the entire batch.
	 */
	version?: number | null;
}
export interface BulkOperationMember7 extends Parsable {
	/**
	 * The action property
	 */
	action?: string | null;
	/**
	 * Item in this collection. Each existing item may occur only once.
	 */
	id?: string | null;
	/**
	 * Current resource lock version; stale versions roll back the entire batch.
	 */
	version?: number | null;
}
export interface BulkRequest extends Parsable {
	/**
	 * The operations property
	 */
	operations?: (BulkOperationMember1 | BulkOperationMember2 | BulkOperationMember3 | BulkOperationMember4 | BulkOperationMember5 | BulkOperationMember6 | BulkOperationMember7)[] | null;
}
export interface BulkResponse extends Parsable {
	/**
	 * The data property
	 */
	data?: BulkResult[] | null;
}
export interface BulkResult extends Parsable {
	/**
	 * The action property
	 */
	action?: BulkResult_action | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * Committed resource lock version, including the tombstone version for deletes.
	 */
	version?: number | null;
}
export type BulkResult_action = (typeof BulkResult_actionObject)[keyof typeof BulkResult_actionObject];
export interface BulkUpdate extends Parsable {
	/**
	 * Attached destination content type. Items only. Prunes unrelated fields, enforces required fields/rules, retains file content and creates a revision. Creation defaults are not applied.
	 */
	contentTypeId?: Guid | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * Item or folder title. Limited to 1024 UTF-16 units before trimming.
	 */
	name?: string | null;
	/**
	 * Destination collection or folder in the same collection.
	 */
	parentId?: string | null;
	/**
	 * The values property
	 */
	values?: BulkUpdate_values | null;
}
export interface BulkUpdate_values extends AdditionalDataHolder, Parsable {
}
export interface CalendarOccurrence extends Parsable {
	/**
	 * The all_day property
	 */
	allDay?: boolean | null;
	/**
	 * The end property
	 */
	end?: Date | null;
	/**
	 * The end_date property
	 */
	endDate?: DateOnly | null;
	/**
	 * The item property
	 */
	item?: Resource | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The start property
	 */
	start?: Date | null;
	/**
	 * The start_date property
	 */
	startDate?: DateOnly | null;
}
export interface CalendarQuery extends Parsable {
	/**
	 * The from property
	 */
	from?: Date | null;
	/**
	 * Collection query over visible surfaces. System fields: $id, $name, $keywords, $created_at, $created_by, $modified_at, $modified_by. Times are RFC3339 UTC and modification metadata follows the selected revision. Archive filters apply before paging, counts, and grouping. $archived_at and $archived_by are system fields usable in columns, filters, and sorting.
	 */
	query?: QuerySpec | null;
	/**
	 * The surface property
	 */
	surface?: CalendarQuery_surface | null;
	/**
	 * The to property
	 */
	to?: Date | null;
}
export type CalendarQuery_surface = (typeof CalendarQuery_surfaceObject)[keyof typeof CalendarQuery_surfaceObject];
/**
 * What text to chunk and how. Give text_id or text.
 */
export interface ChunkText extends Parsable {
	/**
	 * The file version; the item's current file when omitted.
	 */
	blobId?: string | null;
	/**
	 * window (the default): overlapping windows of each page; pages: a chunk per page; paragraphs: paragraphs merged up to the size; headings: sections, each starting with its heading; whole: the whole text as one chunk.
	 */
	chunker?: ChunkText_chunker | null;
	/**
	 * Add the chunks to this generation the caller started for the same file version; a new generation when omitted.
	 */
	generation?: string | null;
	/**
	 * content when omitted.
	 */
	kind?: ChunkText_kind | null;
	/**
	 * Chunk size, 1200 when omitted.
	 */
	maxChars?: number | null;
	/**
	 * 400 when omitted.
	 */
	maxChunks?: number | null;
	/**
	 * Characters consecutive windows share, 150 when omitted.
	 */
	overlap?: number | null;
	/**
	 * Instead of text_id: text given directly, such as a summary.
	 */
	text?: string | null;
	/**
	 * The text_id of a read of the file's text (POST /v1/items/{id}/text).
	 */
	textId?: string | null;
}
export type ChunkText_chunker = (typeof ChunkText_chunkerObject)[keyof typeof ChunkText_chunkerObject];
export type ChunkText_kind = (typeof ChunkText_kindObject)[keyof typeof ChunkText_kindObject];
export interface CollectionContentType extends ContentType, Parsable {
	/**
	 * Default only for this collection.
	 */
	isDefault?: boolean | null;
}
export interface CollectionTemplate extends AdditionalDataHolder, Parsable {
	/**
	 * The allow_folders property
	 */
	allowFolders?: boolean | null;
	/**
	 * The content_types property
	 */
	contentTypes?: TemplateInput[] | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * Libraries only. Default warn. Compares hashes against current documents visible to the caller across libraries and workspaces. Allow suppresses warnings; block rejects duplicates with 409 duplicate_file.
	 */
	duplicatePolicy?: CollectionTemplate_duplicate_policy | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The kind property
	 */
	kind?: CollectionTemplate_kind | null;
	/**
	 * History entries kept per item, trimmed on each new recorded version. Defaults to 50.
	 */
	maxVersions?: number | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The publishing_enabled property
	 */
	publishingEnabled?: boolean | null;
	/**
	 * Collections only. Lists and libraries default to off. Off stops recording new history; retained entries remain available. Replacing a file while off queues its previous bytes for reclamation after the one-hour grace period, subject to current/published file, staged-use and workflow-lease protection.
	 */
	versioning?: CollectionTemplate_versioning | null;
	/**
	 * The views property
	 */
	views?: ViewInput[] | null;
	/**
	 * The webdav_enabled property
	 */
	webdavEnabled?: boolean | null;
}
export type CollectionTemplate_duplicate_policy = (typeof CollectionTemplate_duplicate_policyObject)[keyof typeof CollectionTemplate_duplicate_policyObject];
export type CollectionTemplate_kind = (typeof CollectionTemplate_kindObject)[keyof typeof CollectionTemplate_kindObject];
export type CollectionTemplate_versioning = (typeof CollectionTemplate_versioningObject)[keyof typeof CollectionTemplate_versioningObject];
/**
 * A completion. Give item_id with text_id or image_blob_id, else workspace_id.
 */
export interface CompletionRequest extends Parsable {
	/**
	 * With text_id: the file version; the current one when omitted.
	 */
	blobId?: string | null;
	/**
	 * immediate calls the model now; batch sends the request in a provider batch, which its worker admits in groups per queue. The deployment's default when omitted.
	 */
	execution?: CompletionRequest_execution | null;
	/**
	 * An image file version of the item the model sees, alone or with text.
	 */
	imageBlobId?: string | null;
	/**
	 * Text to work on.
	 */
	input?: string | null;
	/**
	 * The item whose file text_id or image_blob_id are.
	 */
	itemId?: string | null;
	/**
	 * Characters of the input sent, 20000 when omitted.
	 */
	maxChars?: number | null;
	/**
	 * A form schema of an object: the answer is JSON matching it, else text.
	 */
	outputSchema?: CompletionRequest_output_schema | null;
	/**
	 * What to do with the input.
	 */
	prompt?: string | null;
	/**
	 * The batch queue, shared when omitted.
	 */
	queue?: string | null;
	/**
	 * Instead of input: a text read from the item's file version blob_id.
	 */
	textId?: string | null;
	/**
	 * Where a request on text given directly is made.
	 */
	workspaceId?: string | null;
}
export type CompletionRequest_execution = (typeof CompletionRequest_executionObject)[keyof typeof CompletionRequest_executionObject];
/**
 * A form schema of an object: the answer is JSON matching it, else text.
 */
export interface CompletionRequest_output_schema extends AdditionalDataHolder, Parsable {
}
export interface ComposePDF extends Parsable {
	/**
	 * The photo items in page order, the item among them; the item alone when omitted.
	 */
	itemIds?: string[] | null;
	/**
	 * JPEG quality, 80 when omitted.
	 */
	quality?: number | null;
	/**
	 * Height in pixels the smallest text of each page keeps, 32 when omitted; pages never grow.
	 */
	targetHeight?: number | null;
}
export interface ContentType extends AdditionalDataHolder, Parsable {
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * Organization field IDs. Each key must be unique within the content type. Definitions remain live in every attached collection.
	 */
	fieldIds?: Guid[] | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The rules property
	 */
	rules?: (ValidationRuleMember1 | ValidationRuleMember2)[] | null;
	/**
	 * Stable provisioning identity of a registered template content type. Existing customized definitions are reused.
	 */
	templateKey?: string | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
/**
 * Organization catalog definition. Replacement validates all attached collections and advances their schema revisions atomically. Requires contentType.manage, independently of workspace membership.
 */
export interface ContentTypeInput extends Parsable {
	/**
	 * Organization field IDs. Each key must be unique within the content type. Definitions remain live in every attached collection.
	 */
	fieldIds?: Guid[] | null;
	/**
	 * Required for creation, immutable. May be omitted for replacement.
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The rules property
	 */
	rules?: (ValidationRuleMember1 | ValidationRuleMember2)[] | null;
}
declare function createApplyTemplateInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createArchiveEntry_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createArchiveEntryFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createArtifact_metricsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createArtifact_sourcesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createArtifactFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createAttachContentTypeInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createAuditEvent_detailsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createAuditEventFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createAuditPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBlobFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkCreate_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkCreateFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationMember1FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationMember2FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationMember3FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationMember4FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationMember5FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationMember6FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkOperationMember7FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkResponseFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkResultFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkUpdate_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createBulkUpdateFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCalendarOccurrenceFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCalendarQueryFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
export interface CreateCatalogField extends Parsable {
	/**
	 * The choices property
	 */
	choices?: string[] | null;
	/**
	 * Opt-in typed query projection. Changing it queues a background index build; see index_status.
	 */
	indexed?: boolean | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The options property
	 */
	options?: FieldOptions | null;
	/**
	 * The required property
	 */
	required?: boolean | null;
	/**
	 * Immutable decimal scale; zero for all other field types. Scaled units must fit signed 64 bits.
	 */
	scale?: number | null;
	/**
	 * The type property
	 */
	type?: CreateCatalogField_type | null;
}
export type CreateCatalogField_type = (typeof CreateCatalogField_typeObject)[keyof typeof CreateCatalogField_typeObject];
declare function createChunkTextFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCollectionContentTypeFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCollectionTemplateFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCompletionRequest_output_schemaFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCompletionRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createComposePDFFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createContentTypeFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createContentTypeInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateCatalogFieldFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateFieldFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateListIntegration_configurationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateListIntegrationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateRelationship_metadataFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateRelationshipFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateResource_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createCreateResourceFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDecisionRequest_contextFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDecisionRequest_dataFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDecisionRequest_questions_optionsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDecisionRequest_questionsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDecisionRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDeletedResourceFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDeltaPage_valueFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDeltaPage_valueMember1_RemovedFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDeltaPage_valueMember1FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDeltaPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDocumentMetadata_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDocumentMetadataFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDocumentUploadFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createDuplicateDocumentFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
export interface CreateField extends Parsable {
	/**
	 * Unique, nonempty choice strings. No fixed entry-count limit.
	 */
	choices?: string[] | null;
	/**
	 * Collection content type ID. Omission selects the collection default.
	 */
	contentTypeId?: Guid | null;
	/**
	 * Opt-in typed query projection. Changing it queues a background index build; see index_status.
	 */
	indexed?: boolean | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The options property
	 */
	options?: FieldOptions | null;
	/**
	 * The required property
	 */
	required?: boolean | null;
	/**
	 * Immutable decimal scale; zero for all other field types. Scaled units must fit signed 64 bits.
	 */
	scale?: number | null;
	/**
	 * The type property
	 */
	type?: CreateField_type | null;
}
export type CreateField_type = (typeof CreateField_typeObject)[keyof typeof CreateField_typeObject];
declare function createFieldDefinitionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createFieldOptionsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createFileVersionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createFilteredBulkUpdate_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createFilteredBulkUpdateFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createFilterExprFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createGenerationChunkFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createGenerationChunkPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createGenerationEnrichments_enrichmentsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createGenerationEnrichmentsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createItemIntegrationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createItemRevision_payloadFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createItemRevisionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createItemText_pagesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createItemTextFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createKeywordCountFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createKeywordCountListFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createKeywordInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
export interface CreateListIntegration extends Parsable {
	/**
	 * Provider-owned private configuration. For ical_https, supply an HTTPS iCalendar URL.
	 */
	configuration?: CreateListIntegration_configuration | null;
	/**
	 * The kind property
	 */
	kind?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
/**
 * Provider-owned private configuration. For ical_https, supply an HTTPS iCalendar URL.
 */
export interface CreateListIntegration_configuration extends Parsable {
	/**
	 * Public HTTPS iCalendar URL on port 443. Encrypted at rest; never returned.
	 */
	url?: string | null;
}
declare function createListIntegrationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createListViewFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createListViewPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createLocatedItemFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createMergeTermFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createMoveItemFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createMoveTermFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createOperationFailureFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createOperationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createOperationPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createOptimizeImageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPermissions_effective_grantsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPermissions_grantsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPermissionsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPortableSmartFolderDefinitionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPortableSmartFolderFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPortableTerm_labelsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPortableTermFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPortableTermGroupFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPortableTermSetFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createProblem_existing_itemFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createProblemFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPromotedFileFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPromoteKeywordFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPromoteStagedFileFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPublication_snapshotFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createPublicationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createQueryGroupFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createQueryGroupPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createQueryRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createQueryResultFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createQuerySpecFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createReadTextFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createReclaimedFileVersionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createReclaimFileVersionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRecycleBinPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
export interface CreateRelationship extends Parsable {
	/**
	 * Untyped links and new predicates default to symmetric. Explicit direction must match an existing typed policy.
	 */
	directed?: boolean | null;
	/**
	 * The metadata property
	 */
	metadata?: CreateRelationship_metadata | null;
	/**
	 * The target_id property
	 */
	targetId?: Guid | null;
	/**
	 * Optional active relationship term ID, name, localized label or synonym. New labels are ensured in the open set. Typed links require taxonomy.read. Omit for an untyped link.
	 */
	type?: string | null;
}
export interface CreateRelationship_metadata extends AdditionalDataHolder, Parsable {
}
declare function createRelationship_metadataFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipEndpointFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipEndpointPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipNeighborhoodFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipType_attributesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipTypeFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipTypeInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipTypePageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createRelationshipViewFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
export interface CreateResource extends Parsable {
	/**
	 * Lists and libraries only: permits creating physical folders. Disabling retains existing folders. Changes require manage access.
	 */
	allowFolders?: boolean | null;
	/**
	 * Items and folders: an attached collection content type. Creation selects the default when omitted. PATCH changes type, keeps shared fields and removes fields absent from the destination type. Folder required fields and creation defaults do not apply.
	 */
	contentTypeId?: Guid | null;
	/**
	 * Collections only: attach live organization definitions on creation; the first is default. Omission creates an editable Item catalog definition.
	 */
	contentTypeIds?: Guid[] | null;
	/**
	 * Workspace or collection description, limited to 2000 UTF-16 code units. Changes require manage access.
	 */
	description?: string | null;
	/**
	 * Libraries only. Default warn. Compares hashes against current documents visible to the caller across libraries and workspaces. Allow suppresses warnings; block rejects duplicates with 409 duplicate_file.
	 */
	duplicatePolicy?: CreateResource_duplicate_policy | null;
	/**
	 * Optional non-empty caller UUID for items or folders. An existing readable live entry in the same collection is returned unchanged; a different/deleted/inaccessible entry conflicts. Omitting id generates a server UUID.
	 */
	id?: Guid | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * The kind property
	 */
	kind?: CreateResource_kind | null;
	/**
	 * History entries kept per item, trimmed on each new recorded version. Defaults to 50.
	 */
	maxVersions?: number | null;
	/**
	 * Item and folder titles contain at most 1024 UTF-16 units before trimming. Workspace and collection names allow 200 units. Library names also exclude path separators and control characters and are unique among live siblings ignoring case.
	 */
	name?: string | null;
	/**
	 * Lists/libraries only. False publishes content changes immediately. Changing policy requires manage access; disabling publishes all current heads atomically.
	 */
	publishingEnabled?: boolean | null;
	/**
	 * Registered collection template key; provisions settings, shared content types and saved views atomically. Mutually exclusive with content_type_ids. Template settings determine kind and folder, publishing and WebDAV policies.
	 */
	templateKey?: string | null;
	/**
	 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
	 */
	values?: CreateResource_values | null;
	/**
	 * Collections only. Lists and libraries default to off. Off stops recording new history; retained entries remain available. Replacing a file while off queues its previous bytes for reclamation after the one-hour grace period, subject to current/published file, staged-use and workflow-lease protection.
	 */
	versioning?: CreateResource_versioning | null;
	/**
	 * Libraries only. Serves the library's folders and files at /webdav/{id}/.
	 */
	webdavEnabled?: boolean | null;
}
export type CreateResource_duplicate_policy = (typeof CreateResource_duplicate_policyObject)[keyof typeof CreateResource_duplicate_policyObject];
export type CreateResource_kind = (typeof CreateResource_kindObject)[keyof typeof CreateResource_kindObject];
/**
 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
 */
export interface CreateResource_values extends AdditionalDataHolder, Parsable {
}
declare function createResource_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
export type CreateResource_versioning = (typeof CreateResource_versioningObject)[keyof typeof CreateResource_versioningObject];
declare function createResourceAccessFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createResourceFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSchemaRevision_definition_fieldsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSchemaRevision_definitionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSchemaRevisionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSchemaTemplate_definitionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSchemaTemplateFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSchemaTemplatePageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchFacetsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchFacetValueFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchFieldFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchHitFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchStatus_embeddingsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchStatusFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSearchSyncFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSetWorkspaceMemberFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderDefinitionFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderDropFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderEntryFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderGroupByFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderGroupFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderGroupsResultFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderImportResultFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderPackageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderPreviewFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderQueryRequest_sortFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderQueryRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderResultFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderTermRefFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSmartFolderUnclassifyFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createSortSpecFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createStagedArtifactFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createStagedFile_metricsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createStagedFile_sourcesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createStagedFileFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTask_inputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTask_reviewFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTask_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskAnswer_inputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskAnswerFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskCreate_reviewFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskCreate_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskCreateFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskListFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskOutcomeFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskReassignFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskResponseInput_inputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaskResponseInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaxonomyDecisionRequest_contextFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaxonomyDecisionRequest_dataFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaxonomyDecisionRequest_description_overridesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaxonomyDecisionRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaxonomyImportResultFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTaxonomyPackageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTemplateFieldFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTemplateInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTerm_labelsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermEntriesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermGroupFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermGroupInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermGroupPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermImportResultFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermInput_labelsFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermListFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermSetFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermSetInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createTermSetPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createUpdateFieldFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createUpdateListIntegration_configurationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createUpdateListIntegrationFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createUpdateRelationship_metadataFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createUpdateRelationshipFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createUpdateResource_valuesFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createUpdateResourceFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createValidationRuleFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createValidationRuleMember1FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createValidationRuleMember2FromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createViewInputFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createViewQueryRequestFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkflowFormFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkspaceKeywordCountFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkspaceKeywordCountPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkspaceMemberFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkspaceRelationshipPageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkspaceRelationshipTypeFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkspaceRelationshipTypePageFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
declare function createWorkspaceRelationshipViewFromDiscriminatorValue(parseNode: ParseNode | undefined): ((instance?: Parsable) => Record<string, (node: ParseNode) => void>);
/**
 * Questions about text, JSON data or a document: exactly one of input, data or text_id.
 */
export interface DecisionRequest extends Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: string | null;
	/**
	 * With text_id: document context shown with the text.
	 */
	context?: DecisionRequest_context | null;
	/**
	 * JSON data to decide about.
	 */
	data?: DecisionRequest_data | null;
	/**
	 * Text to decide about.
	 */
	input?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The questions property
	 */
	questions?: DecisionRequest_questions[] | null;
	/**
	 * A text read from the item's current file version blob_id.
	 */
	textId?: string | null;
	/**
	 * Where a decision on input or data is made.
	 */
	workspaceId?: string | null;
}
/**
 * With text_id: document context shown with the text.
 */
export interface DecisionRequest_context extends AdditionalDataHolder, Parsable {
}
/**
 * JSON data to decide about.
 */
export interface DecisionRequest_data extends AdditionalDataHolder, Parsable {
}
export interface DecisionRequest_questions extends Parsable {
	/**
	 * The instructions property
	 */
	instructions?: string | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The options property
	 */
	options?: DecisionRequest_questions_options[] | null;
	/**
	 * choice selects an option; noul returns the true probability; score is the expected zero-based ordinal level.
	 */
	type?: DecisionRequest_questions_type | null;
}
export interface DecisionRequest_questions_options extends Parsable {
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The text property
	 */
	text?: string | null;
}
export type DecisionRequest_questions_type = (typeof DecisionRequest_questions_typeObject)[keyof typeof DecisionRequest_questions_typeObject];
export interface DeletedResource extends Parsable, Resource {
	/**
	 * The deleted_at property
	 */
	deletedAt?: Date | null;
	/**
	 * The deleted_by property
	 */
	deletedBy?: string | null;
}
export interface DeltaPage extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataDeltaLink property
	 */
	odataDeltaLink?: string | null;
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: (DeltaPage_valueMember1 | Resource)[] | null;
}
export type DeltaPage_value = DeltaPage_valueMember1 | Resource;
export interface DeltaPage_valueMember1 extends AdditionalDataHolder, Parsable {
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The Removed property
	 */
	removed?: DeltaPage_valueMember1_Removed | null;
}
export interface DeltaPage_valueMember1_Removed extends AdditionalDataHolder, Parsable {
	/**
	 * The reason property
	 */
	reason?: DeltaPage_valueMember1_Removed_reason | null;
}
export type DeltaPage_valueMember1_Removed_reason = (typeof DeltaPage_valueMember1_Removed_reasonObject)[keyof typeof DeltaPage_valueMember1_Removed_reasonObject];
declare function deserializeIntoApplyTemplateInput(applyTemplateInput?: Partial<ApplyTemplateInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoArchiveEntry(archiveEntry?: Partial<ArchiveEntry> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoArchiveEntry_values(archiveEntry_values?: Partial<ArchiveEntry_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoArtifact(artifact?: Partial<Artifact> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoArtifact_metrics(artifact_metrics?: Partial<Artifact_metrics> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoArtifact_sources(artifact_sources?: Partial<Artifact_sources> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoAttachContentTypeInput(attachContentTypeInput?: Partial<AttachContentTypeInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoAuditEvent(auditEvent?: Partial<AuditEvent> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoAuditEvent_details(auditEvent_details?: Partial<AuditEvent_details> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoAuditPage(auditPage?: Partial<AuditPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBlob(blob?: Partial<Blob> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkCreate(bulkCreate?: Partial<BulkCreate> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkCreate_values(bulkCreate_values?: Partial<BulkCreate_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperation(bulkOperation?: Partial<BulkOperationMember1 | BulkOperationMember2 | BulkOperationMember3 | BulkOperationMember4 | BulkOperationMember5 | BulkOperationMember6 | BulkOperationMember7> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperationMember1(bulkOperationMember1?: Partial<BulkOperationMember1> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperationMember2(bulkOperationMember2?: Partial<BulkOperationMember2> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperationMember3(bulkOperationMember3?: Partial<BulkOperationMember3> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperationMember4(bulkOperationMember4?: Partial<BulkOperationMember4> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperationMember5(bulkOperationMember5?: Partial<BulkOperationMember5> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperationMember6(bulkOperationMember6?: Partial<BulkOperationMember6> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkOperationMember7(bulkOperationMember7?: Partial<BulkOperationMember7> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkRequest(bulkRequest?: Partial<BulkRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkResponse(bulkResponse?: Partial<BulkResponse> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkResult(bulkResult?: Partial<BulkResult> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkUpdate(bulkUpdate?: Partial<BulkUpdate> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoBulkUpdate_values(bulkUpdate_values?: Partial<BulkUpdate_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCalendarOccurrence(calendarOccurrence?: Partial<CalendarOccurrence> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCalendarQuery(calendarQuery?: Partial<CalendarQuery> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoChunkText(chunkText?: Partial<ChunkText> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCollectionContentType(collectionContentType?: Partial<CollectionContentType> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCollectionTemplate(collectionTemplate?: Partial<CollectionTemplate> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCompletionRequest(completionRequest?: Partial<CompletionRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCompletionRequest_output_schema(completionRequest_output_schema?: Partial<CompletionRequest_output_schema> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoComposePDF(composePDF?: Partial<ComposePDF> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoContentType(contentType?: Partial<ContentType> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoContentTypeInput(contentTypeInput?: Partial<ContentTypeInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateCatalogField(createCatalogField?: Partial<CreateCatalogField> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateField(createField?: Partial<CreateField> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateListIntegration(createListIntegration?: Partial<CreateListIntegration> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateListIntegration_configuration(createListIntegration_configuration?: Partial<CreateListIntegration_configuration> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateRelationship(createRelationship?: Partial<CreateRelationship> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateRelationship_metadata(createRelationship_metadata?: Partial<CreateRelationship_metadata> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateResource(createResource?: Partial<CreateResource> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoCreateResource_values(createResource_values?: Partial<CreateResource_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDecisionRequest(decisionRequest?: Partial<DecisionRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDecisionRequest_context(decisionRequest_context?: Partial<DecisionRequest_context> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDecisionRequest_data(decisionRequest_data?: Partial<DecisionRequest_data> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDecisionRequest_questions(decisionRequest_questions?: Partial<DecisionRequest_questions> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDecisionRequest_questions_options(decisionRequest_questions_options?: Partial<DecisionRequest_questions_options> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDeletedResource(deletedResource?: Partial<DeletedResource> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDeltaPage(deltaPage?: Partial<DeltaPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDeltaPage_value(deltaPage_value?: Partial<DeltaPage_valueMember1 | Resource> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDeltaPage_valueMember1(deltaPage_valueMember1?: Partial<DeltaPage_valueMember1> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDeltaPage_valueMember1_Removed(deltaPage_valueMember1_Removed?: Partial<DeltaPage_valueMember1_Removed> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDocumentMetadata(documentMetadata?: Partial<DocumentMetadata> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDocumentMetadata_values(documentMetadata_values?: Partial<DocumentMetadata_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDocumentUpload(documentUpload?: Partial<DocumentUpload> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoDuplicateDocument(duplicateDocument?: Partial<DuplicateDocument> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoFieldDefinition(fieldDefinition?: Partial<FieldDefinition> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoFieldOptions(fieldOptions?: Partial<FieldOptions> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoFileVersion(fileVersion?: Partial<FileVersion> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoFilteredBulkUpdate(filteredBulkUpdate?: Partial<FilteredBulkUpdate> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoFilteredBulkUpdate_values(filteredBulkUpdate_values?: Partial<FilteredBulkUpdate_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoFilterExpr(filterExpr?: Partial<FilterExpr> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoGenerationChunk(generationChunk?: Partial<GenerationChunk> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoGenerationChunkPage(generationChunkPage?: Partial<GenerationChunkPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoGenerationEnrichments(generationEnrichments?: Partial<GenerationEnrichments> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoGenerationEnrichments_enrichments(generationEnrichments_enrichments?: Partial<GenerationEnrichments_enrichments> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoItemIntegration(itemIntegration?: Partial<ItemIntegration> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoItemRevision(itemRevision?: Partial<ItemRevision> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoItemRevision_payload(itemRevision_payload?: Partial<ItemRevision_payload> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoItemText(itemText?: Partial<ItemText> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoItemText_pages(itemText_pages?: Partial<ItemText_pages> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoKeywordCount(keywordCount?: Partial<KeywordCount> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoKeywordCountList(keywordCountList?: Partial<KeywordCountList> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoKeywordInput(keywordInput?: Partial<KeywordInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoListIntegration(listIntegration?: Partial<ListIntegration> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoListView(listView?: Partial<ListView> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoListViewPage(listViewPage?: Partial<ListViewPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoLocatedItem(locatedItem?: Partial<LocatedItem> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoMergeTerm(mergeTerm?: Partial<MergeTerm> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoMoveItem(moveItem?: Partial<MoveItem> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoMoveTerm(moveTerm?: Partial<MoveTerm> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoOperation(operation?: Partial<Operation> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoOperationFailure(operationFailure?: Partial<OperationFailure> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoOperationPage(operationPage?: Partial<OperationPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoOptimizeImage(optimizeImage?: Partial<OptimizeImage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPermissions(permissions?: Partial<Permissions> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPermissions_effective_grants(permissions_effective_grants?: Partial<Permissions_effective_grants> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPermissions_grants(permissions_grants?: Partial<Permissions_grants> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPortableSmartFolder(portableSmartFolder?: Partial<PortableSmartFolder> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPortableSmartFolderDefinition(portableSmartFolderDefinition?: Partial<PortableSmartFolderDefinition> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPortableTerm(portableTerm?: Partial<PortableTerm> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPortableTerm_labels(portableTerm_labels?: Partial<PortableTerm_labels> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPortableTermGroup(portableTermGroup?: Partial<PortableTermGroup> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPortableTermSet(portableTermSet?: Partial<PortableTermSet> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoProblem(problem?: Partial<Problem> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoProblem_existing_item(problem_existing_item?: Partial<Problem_existing_item> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPromotedFile(promotedFile?: Partial<PromotedFile> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPromoteKeyword(promoteKeyword?: Partial<PromoteKeyword> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPromoteStagedFile(promoteStagedFile?: Partial<PromoteStagedFile> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPublication(publication?: Partial<Publication> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoPublication_snapshot(publication_snapshot?: Partial<Publication_snapshot> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoQueryGroup(queryGroup?: Partial<QueryGroup> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoQueryGroupPage(queryGroupPage?: Partial<QueryGroupPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoQueryRequest(queryRequest?: Partial<QueryRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoQueryResult(queryResult?: Partial<QueryResult> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoQuerySpec(querySpec?: Partial<QuerySpec> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoReadText(readText?: Partial<ReadText> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoReclaimedFileVersion(reclaimedFileVersion?: Partial<ReclaimedFileVersion> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoReclaimFileVersion(reclaimFileVersion?: Partial<ReclaimFileVersion> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRecycleBinPage(recycleBinPage?: Partial<RecycleBinPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationship(relationship?: Partial<Relationship> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationship_metadata(relationship_metadata?: Partial<Relationship_metadata> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipEndpoint(relationshipEndpoint?: Partial<RelationshipEndpoint> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipEndpointPage(relationshipEndpointPage?: Partial<RelationshipEndpointPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipNeighborhood(relationshipNeighborhood?: Partial<RelationshipNeighborhood> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipType(relationshipType?: Partial<RelationshipType> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipType_attributes(relationshipType_attributes?: Partial<RelationshipType_attributes> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipTypeInput(relationshipTypeInput?: Partial<RelationshipTypeInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipTypePage(relationshipTypePage?: Partial<RelationshipTypePage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoRelationshipView(relationshipView?: Partial<RelationshipView> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoResource(resource?: Partial<Resource> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoResource_values(resource_values?: Partial<Resource_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoResourceAccess(resourceAccess?: Partial<ResourceAccess> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSchemaRevision(schemaRevision?: Partial<SchemaRevision> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSchemaRevision_definition(schemaRevision_definition?: Partial<SchemaRevision_definition> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSchemaRevision_definition_fields(schemaRevision_definition_fields?: Partial<SchemaRevision_definition_fields> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSchemaTemplate(schemaTemplate?: Partial<SchemaTemplate> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSchemaTemplate_definition(schemaTemplate_definition?: Partial<SchemaTemplate_definition> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSchemaTemplatePage(schemaTemplatePage?: Partial<SchemaTemplatePage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchFacets(searchFacets?: Partial<SearchFacets> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchFacetValue(searchFacetValue?: Partial<SearchFacetValue> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchField(searchField?: Partial<SearchField> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchHit(searchHit?: Partial<SearchHit> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchPage(searchPage?: Partial<SearchPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchRequest(searchRequest?: Partial<SearchRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchStatus(searchStatus?: Partial<SearchStatus> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchStatus_embeddings(searchStatus_embeddings?: Partial<SearchStatus_embeddings> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSearchSync(searchSync?: Partial<SearchSync> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSetWorkspaceMember(setWorkspaceMember?: Partial<SetWorkspaceMember> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolder(smartFolder?: Partial<SmartFolder> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderDefinition(smartFolderDefinition?: Partial<SmartFolderDefinition> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderDrop(smartFolderDrop?: Partial<SmartFolderDrop> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderEntry(smartFolderEntry?: Partial<SmartFolderEntry> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderGroup(smartFolderGroup?: Partial<SmartFolderGroup> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderGroupBy(smartFolderGroupBy?: Partial<SmartFolderGroupBy> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderGroupsResult(smartFolderGroupsResult?: Partial<SmartFolderGroupsResult> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderImportResult(smartFolderImportResult?: Partial<SmartFolderImportResult> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderInput(smartFolderInput?: Partial<SmartFolderInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderPackage(smartFolderPackage?: Partial<SmartFolderPackage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderPage(smartFolderPage?: Partial<SmartFolderPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderPreview(smartFolderPreview?: Partial<SmartFolderPreview> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderQueryRequest(smartFolderQueryRequest?: Partial<SmartFolderQueryRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderQueryRequest_sort(smartFolderQueryRequest_sort?: Partial<SmartFolderQueryRequest_sort> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderResult(smartFolderResult?: Partial<SmartFolderResult> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderTermRef(smartFolderTermRef?: Partial<SmartFolderTermRef> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSmartFolderUnclassify(smartFolderUnclassify?: Partial<SmartFolderUnclassify> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoSortSpec(sortSpec?: Partial<SortSpec> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoStagedArtifact(stagedArtifact?: Partial<StagedArtifact> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoStagedFile(stagedFile?: Partial<StagedFile> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoStagedFile_metrics(stagedFile_metrics?: Partial<StagedFile_metrics> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoStagedFile_sources(stagedFile_sources?: Partial<StagedFile_sources> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTask(task?: Partial<Task> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTask_input(task_input?: Partial<Task_input> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTask_review(task_review?: Partial<Task_review> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTask_values(task_values?: Partial<Task_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskAnswer(taskAnswer?: Partial<TaskAnswer> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskAnswer_input(taskAnswer_input?: Partial<TaskAnswer_input> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskCreate(taskCreate?: Partial<TaskCreate> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskCreate_review(taskCreate_review?: Partial<TaskCreate_review> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskCreate_values(taskCreate_values?: Partial<TaskCreate_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskList(taskList?: Partial<TaskList> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskOutcome(taskOutcome?: Partial<TaskOutcome> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskReassign(taskReassign?: Partial<TaskReassign> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskResponseInput(taskResponseInput?: Partial<TaskResponseInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaskResponseInput_input(taskResponseInput_input?: Partial<TaskResponseInput_input> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaxonomyDecisionRequest(taxonomyDecisionRequest?: Partial<TaxonomyDecisionRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaxonomyDecisionRequest_context(taxonomyDecisionRequest_context?: Partial<TaxonomyDecisionRequest_context> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaxonomyDecisionRequest_data(taxonomyDecisionRequest_data?: Partial<TaxonomyDecisionRequest_data> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaxonomyDecisionRequest_description_overrides(taxonomyDecisionRequest_description_overrides?: Partial<TaxonomyDecisionRequest_description_overrides> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaxonomyImportResult(taxonomyImportResult?: Partial<TaxonomyImportResult> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTaxonomyPackage(taxonomyPackage?: Partial<TaxonomyPackage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTemplateField(templateField?: Partial<TemplateField> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTemplateInput(templateInput?: Partial<TemplateInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTerm(term?: Partial<Term> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTerm_labels(term_labels?: Partial<Term_labels> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermEntries(termEntries?: Partial<TermEntries> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermGroup(termGroup?: Partial<TermGroup> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermGroupInput(termGroupInput?: Partial<TermGroupInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermGroupPage(termGroupPage?: Partial<TermGroupPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermImportResult(termImportResult?: Partial<TermImportResult> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermInput(termInput?: Partial<TermInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermInput_labels(termInput_labels?: Partial<TermInput_labels> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermList(termList?: Partial<TermList> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermPage(termPage?: Partial<TermPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermSet(termSet?: Partial<TermSet> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermSetInput(termSetInput?: Partial<TermSetInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoTermSetPage(termSetPage?: Partial<TermSetPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoUpdateField(updateField?: Partial<UpdateField> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoUpdateListIntegration(updateListIntegration?: Partial<UpdateListIntegration> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoUpdateListIntegration_configuration(updateListIntegration_configuration?: Partial<UpdateListIntegration_configuration> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoUpdateRelationship(updateRelationship?: Partial<UpdateRelationship> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoUpdateRelationship_metadata(updateRelationship_metadata?: Partial<UpdateRelationship_metadata> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoUpdateResource(updateResource?: Partial<UpdateResource> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoUpdateResource_values(updateResource_values?: Partial<UpdateResource_values> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoValidationRule(validationRule?: Partial<ValidationRuleMember1 | ValidationRuleMember2> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoValidationRuleMember1(validationRuleMember1?: Partial<ValidationRuleMember1> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoValidationRuleMember2(validationRuleMember2?: Partial<ValidationRuleMember2> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoViewInput(viewInput?: Partial<ViewInput> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoViewQueryRequest(viewQueryRequest?: Partial<ViewQueryRequest> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkflowForm(workflowForm?: Partial<WorkflowForm> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkspaceKeywordCount(workspaceKeywordCount?: Partial<WorkspaceKeywordCount> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkspaceKeywordCountPage(workspaceKeywordCountPage?: Partial<WorkspaceKeywordCountPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkspaceMember(workspaceMember?: Partial<WorkspaceMember> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkspaceRelationshipPage(workspaceRelationshipPage?: Partial<WorkspaceRelationshipPage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkspaceRelationshipType(workspaceRelationshipType?: Partial<WorkspaceRelationshipType> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkspaceRelationshipTypePage(workspaceRelationshipTypePage?: Partial<WorkspaceRelationshipTypePage> | undefined): Record<string, (node: ParseNode) => void>;
declare function deserializeIntoWorkspaceRelationshipView(workspaceRelationshipView?: Partial<WorkspaceRelationshipView> | undefined): Record<string, (node: ParseNode) => void>;
export interface DocumentMetadata extends Parsable {
	/**
	 * An attached library content type; defaults to the library default.
	 */
	contentTypeId?: Guid | null;
	/**
	 * A writable folder in this library; defaults to the root.
	 */
	folderId?: Guid | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * Optional lowercase OCR codes joined by '+', e.g. eng+deu; surrounding whitespace is trimmed. At most six codes, each 2–31 letters/underscores.
	 */
	languages?: string | null;
	/**
	 * Item name; defaults to the uploaded filename. Limited to 1024 UTF-16 units before trimming.
	 */
	name?: string | null;
	/**
	 * Custom field values, validated against the selected type. Exact JSON numbers are preserved.
	 */
	values?: DocumentMetadata_values | null;
}
/**
 * Custom field values, validated against the selected type. Exact JSON numbers are preserved.
 */
export interface DocumentMetadata_values extends AdditionalDataHolder, Parsable {
}
export interface DocumentUpload extends AdditionalDataHolder, Parsable {
	/**
	 * The duplicates property
	 */
	duplicates?: DuplicateDocument[] | null;
	/**
	 * The file property
	 */
	file?: Blob | null;
	/**
	 * The item property
	 */
	item?: Resource | null;
}
export interface DuplicateDocument extends AdditionalDataHolder, Parsable {
	/**
	 * The collection_id property
	 */
	collectionId?: Guid | null;
	/**
	 * The item_id property
	 */
	itemId?: Guid | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
/**
 * Organization field definition referenced by content types. Changes reach every attached collection; index_status stays building until all collection operations finish.
 */
export interface FieldDefinition extends AdditionalDataHolder, Parsable {
	/**
	 * Unique, nonempty choice strings. No fixed entry-count limit.
	 */
	choices?: string[] | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * Opt-in typed query projection. Changing it queues a background index build; see index_status.
	 */
	indexed?: boolean | null;
	/**
	 * ready when the field's index rows match indexed. Queries reject an indexed field until its index is ready.
	 */
	indexStatus?: FieldDefinition_index_status | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The options property
	 */
	options?: FieldOptions | null;
	/**
	 * The required property
	 */
	required?: boolean | null;
	/**
	 * Immutable decimal scale; zero for all other field types. Scaled units must fit signed 64 bits.
	 */
	scale?: number | null;
	/**
	 * The type property
	 */
	type?: FieldDefinition_type | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export type FieldDefinition_index_status = (typeof FieldDefinition_index_statusObject)[keyof typeof FieldDefinition_index_statusObject];
export type FieldDefinition_type = (typeof FieldDefinition_typeObject)[keyof typeof FieldDefinition_typeObject];
export interface FieldOptions extends Parsable {
	/**
	 * Required for currency fields only. Three uppercase letters, such as EUR. Currency and number values are exact JSON decimals with a 96-bit coefficient and scale 0..28; excess fractional precision rounds to nearest, ties to even.
	 */
	currencyCode?: string | null;
	/**
	 * The default_value property
	 */
	defaultValue?: UntypedNode | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * Existing live organization collection. May be in any workspace. Explicit lookup assignments and defaults require live non-folder items in this collection, including IDs already stored on the item. Omitted references are retained on unrelated edits. Validation does not require target read permission or grant access to target content.
	 */
	lookupContainerId?: string | null;
	/**
	 * Exact numeric upper bound.
	 */
	maximum?: string | null;
	/**
	 * Length in UTF-16 code units (non-BMP characters count as two). Text/email/URL ceiling 4000; note ceiling 1000000. Defaults: text/email 255, URL 2048, note 100000. Valid only for these four field types.
	 */
	maxLength?: number | null;
	/**
	 * Exact numeric lower bound.
	 */
	minimum?: string | null;
	/**
	 * Array values are supported only for choice, person, lookup, term and keywords. Text, email, URL and other scalar types reject multiple.
	 */
	multiple?: boolean | null;
	/**
	 * Where the field's text goes in search: high ranks it with names and keywords, none leaves it out (the field stays filterable). Applies to entries as they are reindexed.
	 */
	searchWeight?: FieldOptions_search_weight | null;
	/**
	 * The term_set_id property
	 */
	termSetId?: string | null;
	/**
	 * Collection-wide exact, case-sensitive business key covering both head and published values. Requires indexed=true and a scalar type. Numeric equality uses canonical exact values. Nulls do not reserve a key. Collisions return 409 and roll back the entire mutation or bulk batch.
	 */
	unique?: boolean | null;
}
export type FieldOptions_search_weight = (typeof FieldOptions_search_weightObject)[keyof typeof FieldOptions_search_weightObject];
export interface FileVersion extends AdditionalDataHolder, Parsable {
	/**
	 * available while the bytes are kept, pending after reclamation is requested, or reclaimed after the grace period. A reclaimed version keeps its identity and metadata; its download and restore report that the content is unavailable.
	 */
	availability?: FileVersion_availability | null;
	/**
	 * The content_type property
	 */
	contentType?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * Subject that uploaded or restored this version.
	 */
	createdBy?: string | null;
	/**
	 * The filename property
	 */
	filename?: string | null;
	/**
	 * Blob id, as in revision blob_id.
	 */
	id?: Guid | null;
	/**
	 * The is_current property
	 */
	isCurrent?: boolean | null;
	/**
	 * The sha256 property
	 */
	sha256?: string | null;
	/**
	 * The size property
	 */
	size?: number | null;
	/**
	 * File version number used by restore.
	 */
	version?: number | null;
}
export type FileVersion_availability = (typeof FileVersion_availabilityObject)[keyof typeof FileVersion_availabilityObject];
/**
 * At least one of name, keywords or non-empty values is required. Query is optional and uses head content.
 */
export interface FilteredBulkUpdate extends Parsable {
	/**
	 * Attached destination content type. Items only. Prunes unrelated fields, enforces required fields/rules, retains file content and creates a revision. Creation defaults are not applied.
	 */
	contentTypeId?: Guid | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * Collection query over visible surfaces. System fields: $id, $name, $keywords, $created_at, $created_by, $modified_at, $modified_by. Times are RFC3339 UTC and modification metadata follows the selected revision. Archive filters apply before paging, counts, and grouping. $archived_at and $archived_by are system fields usable in columns, filters, and sorting.
	 */
	query?: QuerySpec | null;
	/**
	 * Field patch: omitted fields remain unchanged, null removes a field. Each item is validated against its own current content type.
	 */
	values?: FilteredBulkUpdate_values | null;
}
/**
 * Field patch: omitted fields remain unchanged, null removes a field. Each item is validated against its own current content type.
 */
export interface FilteredBulkUpdate_values extends AdditionalDataHolder, Parsable {
}
/**
 * One condition or AND/OR/NOT group; maximum 32 nodes and depth 6. Custom fields must be indexed. $id/$name are system fields; $keywords supports equality. Multi-valued eq matches any member; ne requires present with no equal member. Collection and smart-folder rules also accept $file_type for the MIME type of the visible file revision.
 */
export interface FilterExpr extends Parsable {
	/**
	 * The and property
	 */
	and?: FilterExpr[] | null;
	/**
	 * The field property
	 */
	field?: string | null;
	/**
	 * One condition or AND/OR/NOT group; maximum 32 nodes and depth 6. Custom fields must be indexed. $id/$name are system fields; $keywords supports equality. Multi-valued eq matches any member; ne requires present with no equal member. Collection and smart-folder rules also accept $file_type for the MIME type of the visible file revision.
	 */
	not?: FilterExpr | null;
	/**
	 * The op property
	 */
	op?: FilterExpr_op | null;
	/**
	 * The or property
	 */
	or?: FilterExpr[] | null;
	/**
	 * The value property
	 */
	value?: UntypedNode | null;
	/**
	 * Exclusive with value. me resolves to the authenticated subject; UTC date references require date/datetime fields and resolve to midnight for datetime. Plain value strings stay literal.
	 */
	valueRef?: FilterExpr_value_ref | null;
}
export type FilterExpr_op = (typeof FilterExpr_opObject)[keyof typeof FilterExpr_opObject];
export type FilterExpr_value_ref = (typeof FilterExpr_value_refObject)[keyof typeof FilterExpr_value_refObject];
export interface GenerationChunk extends Parsable {
	/**
	 * The enrichment property
	 */
	enrichment?: string | null;
	/**
	 * The kind property
	 */
	kind?: string | null;
	/**
	 * The ordinal property
	 */
	ordinal?: number | null;
	/**
	 * The page property
	 */
	page?: number | null;
	/**
	 * The chunk operation that staged the chunk.
	 */
	part?: string | null;
	/**
	 * The text property
	 */
	text?: string | null;
	/**
	 * The text was cut to keep the page within its budget.
	 */
	truncated?: boolean | null;
}
export interface GenerationChunkPage extends Parsable {
	/**
	 * The chunks property
	 */
	chunks?: GenerationChunk[] | null;
	/**
	 * The more property
	 */
	more?: boolean | null;
	/**
	 * The next_offset property
	 */
	nextOffset?: number | null;
}
export interface GenerationEnrichments extends Parsable {
	/**
	 * The enrichments property
	 */
	enrichments?: GenerationEnrichments_enrichments[] | null;
}
export interface GenerationEnrichments_enrichments extends Parsable {
	/**
	 * The ordinal property
	 */
	ordinal?: number | null;
	/**
	 * The part property
	 */
	part?: string | null;
	/**
	 * The text property
	 */
	text?: string | null;
}
export interface ItemIntegration extends Parsable {
	/**
	 * The integration_id property
	 */
	integrationId?: Guid | null;
	/**
	 * The managed_fields property
	 */
	managedFields?: string[] | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
export interface ItemRevision extends AdditionalDataHolder, Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: Guid | null;
	/**
	 * Sorted fields changed from the preceding revision. $name and $keywords identify system metadata. File blob changes are not included.
	 */
	changedFields?: string[] | null;
	/**
	 * The container_id property
	 */
	containerId?: Guid | null;
	/**
	 * The content_type_id property
	 */
	contentTypeId?: Guid | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The created_by property
	 */
	createdBy?: string | null;
	/**
	 * Attachment display name frozen in this revision; empty without a file.
	 */
	filename?: string | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * True for the newest retained recorded version, including while recording is off.
	 */
	isCurrent?: boolean | null;
	/**
	 * The item_id property
	 */
	itemId?: Guid | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * Item and folder titles contain at most 1024 UTF-16 units before trimming. Workspace and collection names allow 200 units. Library names also exclude path separators and control characters and are unique among live siblings ignoring case.
	 */
	name?: string | null;
	/**
	 * The payload property
	 */
	payload?: ItemRevision_payload | null;
	/**
	 * Recorded history number. Increments only for recorded versions, independently of internal content snapshots.
	 */
	revisionNumber?: number | null;
	/**
	 * Schema snapshot included in revision history for interpreting historical values.
	 */
	schemaRevision?: SchemaRevision | null;
	/**
	 * The schema_revision_id property
	 */
	schemaRevisionId?: Guid | null;
}
export interface ItemRevision_payload extends AdditionalDataHolder, Parsable {
}
export interface ItemText extends Parsable {
	/**
	 * The file version read.
	 */
	blobId?: string | null;
	/**
	 * The chars property
	 */
	chars?: number | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The language property
	 */
	language?: string | null;
	/**
	 * The ocr_pages property
	 */
	ocrPages?: number | null;
	/**
	 * The page_count property
	 */
	pageCount?: number | null;
	/**
	 * The pages property
	 */
	pages?: ItemText_pages[] | null;
	/**
	 * The read_at property
	 */
	readAt?: Date | null;
	/**
	 * Pages without a text layer that were not OCRed.
	 */
	skippedPages?: number | null;
	/**
	 * The text_id property
	 */
	textId?: string | null;
	/**
	 * The truncated property
	 */
	truncated?: boolean | null;
}
export interface ItemText_pages extends Parsable {
	/**
	 * The ocr property
	 */
	ocr?: boolean | null;
	/**
	 * The page property
	 */
	page?: number | null;
	/**
	 * The text property
	 */
	text?: string | null;
}
export interface KeywordCount extends Parsable {
	/**
	 * Items whose current content holds the keyword.
	 */
	count?: number | null;
	/**
	 * The term property
	 */
	term?: Term | null;
}
export interface KeywordCountList extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: KeywordCount[] | null;
}
export interface KeywordInput extends Parsable {
	/**
	 * The name property
	 */
	name?: string | null;
}
export interface ListIntegration extends Parsable {
	/**
	 * The container_id property
	 */
	containerId?: Guid | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The error property
	 */
	errorEscaped?: string | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The item_count property
	 */
	itemCount?: number | null;
	/**
	 * The kind property
	 */
	kind?: ListIntegration_kind | null;
	/**
	 * The last_attempt_at property
	 */
	lastAttemptAt?: Date | null;
	/**
	 * The last_success_at property
	 */
	lastSuccessAt?: Date | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The paused property
	 */
	paused?: boolean | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export type ListIntegration_kind = (typeof ListIntegration_kindObject)[keyof typeof ListIntegration_kindObject];
export interface ListView extends Parsable {
	/**
	 * The columns property
	 */
	columns?: string[] | null;
	/**
	 * The container_id property
	 */
	containerId?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The is_default property
	 */
	isDefault?: boolean | null;
	/**
	 * The layout property
	 */
	layout?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * Collection query over visible surfaces. System fields: $id, $name, $keywords, $created_at, $created_by, $modified_at, $modified_by. Times are RFC3339 UTC and modification metadata follows the selected revision. Archive filters apply before paging, counts, and grouping. $archived_at and $archived_by are system fields usable in columns, filters, and sorting.
	 */
	query?: QuerySpec | null;
	/**
	 * Protected built-in view; cannot be modified, deleted, or selected as the opening default.
	 */
	systemKey?: ListView_system_key | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export type ListView_system_key = (typeof ListView_system_keyObject)[keyof typeof ListView_system_keyObject];
export interface ListViewPage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: ListView[] | null;
}
export interface LocatedItem extends Parsable, Resource {
	/**
	 * The collection_id property
	 */
	collectionId?: Guid | null;
	/**
	 * The collection_name property
	 */
	collectionName?: string | null;
	/**
	 * The workspace_name property
	 */
	workspaceName?: string | null;
}
export interface MergeTerm extends Parsable {
	/**
	 * The target_term_id property
	 */
	targetTermId?: string | null;
}
export interface MoveItem extends Parsable {
	/**
	 * The collection_id property
	 */
	collectionId?: Guid | null;
	/**
	 * Destination folder ID; empty or omitted selects the collection root.
	 */
	parentId?: string | null;
}
export interface MoveTerm extends Parsable {
	/**
	 * New parent in the same set; null moves the term to the root.
	 */
	parentId?: string | null;
}
/**
 * Tracked operation executed by a registered handler, in bounded transactional batches or outside the database writer under a renewed claim. Field indexing and filtered bulk edits are built in. Payloads, credential constraints and claims are private; progress and results are visible through operation reads. Pending work resumes after restart: an execution that stops gives its claim back or lets it expire, and transient failures retry later. Terminal records expire after 30 days without an update.
 */
export interface Operation extends AdditionalDataHolder, Parsable {
	/**
	 * Executions that began, including the current one.
	 */
	attempts?: number | null;
	/**
	 * The completed_at property
	 */
	completedAt?: Date | null;
	/**
	 * The container_id property
	 */
	containerId?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The created_by property
	 */
	createdBy?: string | null;
	/**
	 * Latest completion; past it the operation fails with deadline_exceeded.
	 */
	deadline?: Date | null;
	/**
	 * Why a failed operation failed, when the reason is one clients act on: its deadline passed, its retries ran out, or its creator lost permission.
	 */
	errorCode?: Operation_error_code | null;
	/**
	 * The error property
	 */
	errorEscaped?: string | null;
	/**
	 * All per-item failures, including failures beyond the reported detail limit.
	 */
	failed?: number | null;
	/**
	 * The failures property
	 */
	failures?: OperationFailure[] | null;
	/**
	 * Catalog field ID for field_index; empty for bulk_update.
	 */
	fieldId?: string | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * Registered operation kind; field_index and bulk_update are built in, and extensions register others, such as search.reindex.
	 */
	kind?: string | null;
	/**
	 * Frozen number of selected items; populated when execution begins.
	 */
	matched?: number | null;
	/**
	 * When a waiting operation is next attempted.
	 */
	nextAttemptAt?: Date | null;
	/**
	 * The operation that started this one and owns it: cancelling the parent cancels it.
	 */
	parentId?: string | null;
	/**
	 * Surfaces processed for field_index; items completed (updated + failed) for bulk_update.
	 */
	processed?: number | null;
	/**
	 * Completed items as an integer percentage; reaches 100 on completion, including zero matches.
	 */
	progress?: number | null;
	/**
	 * Handler result; handlers may expose partial results before completion.
	 */
	result?: UntypedNode | null;
	/**
	 * The started_at property
	 */
	startedAt?: Date | null;
	/**
	 * superseded: replaced by newer work; cancelled: withdrawn by its creator or an organization manager.
	 */
	status?: Operation_status | null;
	/**
	 * The trace_id property
	 */
	traceId?: string | null;
	/**
	 * Successfully committed items.
	 */
	updated?: number | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * Why a pending operation has not finished: queued for a worker, running now, waiting to retry after a transient failure, scheduled for a later attempt, or waiting for remote work it joined.
	 */
	waiting?: Operation_waiting | null;
	/**
	 * Workspace the operation belongs to; absent for organization work.
	 */
	workspaceId?: string | null;
}
export type Operation_error_code = (typeof Operation_error_codeObject)[keyof typeof Operation_error_codeObject];
export type Operation_status = (typeof Operation_statusObject)[keyof typeof Operation_statusObject];
export type Operation_waiting = (typeof Operation_waitingObject)[keyof typeof Operation_waitingObject];
export interface OperationFailure extends AdditionalDataHolder, Parsable {
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The reason property
	 */
	reason?: string | null;
}
export interface OperationPage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: Operation[] | null;
}
export interface OptimizeImage extends Parsable {
	/**
	 * The file version the workflow was started for: a newer one is kept as it is.
	 */
	fileVersion?: number | null;
	/**
	 * Longest side of the copy analyzed, 2600 when omitted.
	 */
	maxAnalysisDimension?: number | null;
	/**
	 * Detector score a text line needs, 50 when omitted.
	 */
	minConfidence?: number | null;
	/**
	 * How far the image may shrink, 0.05 when omitted.
	 */
	minimumScale?: number | null;
	/**
	 * Percentile of the text heights taken as the smallest text, 5 when omitted.
	 */
	percentile?: number | null;
	/**
	 * WebP quality, 80 when omitted.
	 */
	quality?: number | null;
	/**
	 * Height in pixels the smallest text keeps, 12 when omitted.
	 */
	targetHeight?: number | null;
}
/**
 * SharePoint-style exclusive scopes. An inherited resource has no local grants. Grants at the nearest exclusive ancestor apply; ancestors beyond it do not. Grants within a scope are additive. Reset with inherit=true and no grants. Denies are rejected.
 */
export interface Permissions extends Parsable {
	/**
	 * Break inheritance and copy the effective inherited grants. Requires inherit=false.
	 */
	copyInherited?: boolean | null;
	/**
	 * The effective_grants property
	 */
	effectiveGrants?: Permissions_effective_grants[] | null;
	/**
	 * The grants property
	 */
	grants?: Permissions_grants[] | null;
	/**
	 * The inherit property
	 */
	inherit?: boolean | null;
	/**
	 * Nearest exclusive scope that supplies permissions.
	 */
	scopeId?: string | null;
}
export interface Permissions_effective_grants extends Parsable {
	/**
	 * The action property
	 */
	action?: Permissions_effective_grants_action | null;
	/**
	 * The effect property
	 */
	effect?: Permissions_effective_grants_effect | null;
	/**
	 * Enrolled subject, existing group ID, or the resource workspace ID for workspace role principals.
	 */
	principalId?: string | null;
	/**
	 * The principal_type property
	 */
	principalType?: Permissions_effective_grants_principal_type | null;
}
export type Permissions_effective_grants_action = (typeof Permissions_effective_grants_actionObject)[keyof typeof Permissions_effective_grants_actionObject];
export type Permissions_effective_grants_effect = (typeof Permissions_effective_grants_effectObject)[keyof typeof Permissions_effective_grants_effectObject];
export type Permissions_effective_grants_principal_type = (typeof Permissions_effective_grants_principal_typeObject)[keyof typeof Permissions_effective_grants_principal_typeObject];
export interface Permissions_grants extends Parsable {
	/**
	 * The action property
	 */
	action?: Permissions_grants_action | null;
	/**
	 * The effect property
	 */
	effect?: Permissions_grants_effect | null;
	/**
	 * Enrolled subject, existing group ID, or the resource workspace ID for workspace role principals.
	 */
	principalId?: string | null;
	/**
	 * The principal_type property
	 */
	principalType?: Permissions_grants_principal_type | null;
}
export type Permissions_grants_action = (typeof Permissions_grants_actionObject)[keyof typeof Permissions_grants_actionObject];
export type Permissions_grants_effect = (typeof Permissions_grants_effectObject)[keyof typeof Permissions_grants_effectObject];
export type Permissions_grants_principal_type = (typeof Permissions_grants_principal_typeObject)[keyof typeof Permissions_grants_principal_typeObject];
export interface PortableSmartFolder extends Parsable {
	/**
	 * Portable shared definition: named selectors and term paths, with filter literals retained literally.
	 */
	definition?: PortableSmartFolderDefinition | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
/**
 * Portable shared definition: named selectors and term paths, with filter literals retained literally.
 */
export interface PortableSmartFolderDefinition extends Parsable {
	/**
	 * Discovery state. Omitted selects active items; archive status is independent of publication and deletion.
	 */
	archive?: ArchiveSelection | null;
	/**
	 * Omit to search both lists and libraries.
	 */
	collectionKind?: PortableSmartFolderDefinition_collection_kind | null;
	/**
	 * The collections property
	 */
	collections?: string[] | null;
	/**
	 * The content_types property
	 */
	contentTypes?: string[] | null;
	/**
	 * One condition or AND/OR/NOT group; maximum 32 nodes and depth 6. Custom fields must be indexed. $id/$name are system fields; $keywords supports equality. Multi-valued eq matches any member; ne requires present with no equal member. Collection and smart-folder rules also accept $file_type for the MIME type of the visible file revision.
	 */
	filter?: FilterExpr | null;
	/**
	 * The group_by property
	 */
	groupBy?: SmartFolderGroupBy[] | null;
	/**
	 * Include live physical folders, including their content types, typed metadata and taxonomy. Omitted/false selects items only.
	 */
	includeFolders?: boolean | null;
	/**
	 * query saves a read-only live search; classification also permits explicit classify/unclassify actions.
	 */
	mode?: PortableSmartFolderDefinition_mode | null;
	/**
	 * The templates property
	 */
	templates?: string[] | null;
	/**
	 * The term_match property
	 */
	termMatch?: PortableSmartFolderDefinition_term_match | null;
	/**
	 * The terms property
	 */
	terms?: SmartFolderTermRef[] | null;
}
export type PortableSmartFolderDefinition_collection_kind = (typeof PortableSmartFolderDefinition_collection_kindObject)[keyof typeof PortableSmartFolderDefinition_collection_kindObject];
export type PortableSmartFolderDefinition_mode = (typeof PortableSmartFolderDefinition_modeObject)[keyof typeof PortableSmartFolderDefinition_modeObject];
export type PortableSmartFolderDefinition_term_match = (typeof PortableSmartFolderDefinition_term_matchObject)[keyof typeof PortableSmartFolderDefinition_term_matchObject];
export interface PortableTerm extends Parsable {
	/**
	 * The available_as_keyword property
	 */
	availableAsKeyword?: boolean | null;
	/**
	 * The children property
	 */
	children?: PortableTerm[] | null;
	/**
	 * The color property
	 */
	color?: string | null;
	/**
	 * The deprecated property
	 */
	deprecated?: boolean | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The labels property
	 */
	labels?: PortableTerm_labels | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The sort_order property
	 */
	sortOrder?: number | null;
	/**
	 * The synonyms property
	 */
	synonyms?: string[] | null;
}
export interface PortableTerm_labels extends AdditionalDataHolder, Parsable {
}
export interface PortableTermGroup extends Parsable {
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The sets property
	 */
	sets?: PortableTermSet[] | null;
	/**
	 * The system group, matched by role rather than name.
	 */
	system?: boolean | null;
}
export interface PortableTermSet extends Parsable {
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The is_open property
	 */
	isOpen?: boolean | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The terms property
	 */
	terms?: PortableTerm[] | null;
}
export interface Problem extends ApiError, Parsable {
	/**
	 * The code property
	 */
	code?: string | null;
	/**
	 * The detail property
	 */
	detail?: string | null;
	/**
	 * On an upload or move-selection name_conflict, the existing writable file available for conditional replacement. Omitted for inaccessible items and folders.
	 */
	existingItem?: Problem_existing_item | null;
	/**
	 * Zero-based failing operation index for bulk item errors. Omitted for batch-wide errors.
	 */
	operationIndex?: number | null;
	/**
	 * The request_id property
	 */
	requestId?: string | null;
	/**
	 * The supplied source id of a move-selection name conflict.
	 */
	sourceItemId?: Guid | null;
	/**
	 * The status property
	 */
	status?: number | null;
	/**
	 * The title property
	 */
	title?: string | null;
	/**
	 * The type property
	 */
	type?: string | null;
}
/**
 * On an upload or move-selection name_conflict, the existing writable file available for conditional replacement. Omitted for inaccessible items and folders.
 */
export interface Problem_existing_item extends Parsable {
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface PromotedFile extends Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: string | null;
	/**
	 * The file_version property
	 */
	fileVersion?: number | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
}
export interface PromoteKeyword extends Parsable {
	/**
	 * The parent_id property
	 */
	parentId?: string | null;
	/**
	 * The term_set_id property
	 */
	termSetId?: string | null;
}
export interface PromoteStagedFile extends Parsable {
	/**
	 * Move the other source items to the recycle bin with the promotion.
	 */
	recycleSources?: boolean | null;
}
export interface Publication extends AdditionalDataHolder, Parsable {
	/**
	 * The action property
	 */
	action?: Publication_action | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The published_by property
	 */
	publishedBy?: string | null;
	/**
	 * Optional revision link. Workspace copies omit links to collected revisions; the immutable snapshot remains available.
	 */
	revisionId?: Guid | null;
	/**
	 * The snapshot property
	 */
	snapshot?: Publication_snapshot | null;
	/**
	 * Resource lock version consumed by this event. Explicit lifecycle returns ETag version+1.
	 */
	version?: number | null;
}
export type Publication_action = (typeof Publication_actionObject)[keyof typeof Publication_actionObject];
export interface Publication_snapshot extends AdditionalDataHolder, Parsable {
}
/**
 * Grouping key and authorized count before pagination. Integer and decimal keys are exact strings, booleans are booleans, absent values are null.
 */
export interface QueryGroup extends Parsable {
	/**
	 * The count property
	 */
	count?: number | null;
	/**
	 * Name of the term of a term or keywords group, when the caller has taxonomy.read.
	 */
	label?: string | null;
	/**
	 * The value property
	 */
	value?: UntypedNode | null;
}
export interface QueryGroupPage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The search text matched more than 10,000 entries; the page covers only the first 10,000 matches. Narrow the search.
	 */
	searchCapped?: boolean | null;
	/**
	 * The value property
	 */
	value?: QueryGroup[] | null;
}
/**
 * Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
 */
export interface QueryRequest extends Parsable {
	/**
	 * Count every authorized match before pagination. An exact total evaluates the whole result set, so it is returned only when requested.
	 */
	includeTotal?: boolean | null;
	/**
	 * Collection query over visible surfaces. System fields: $id, $name, $keywords, $created_at, $created_by, $modified_at, $modified_by. Times are RFC3339 UTC and modification metadata follows the selected revision. Archive filters apply before paging, counts, and grouping. $archived_at and $archived_by are system fields usable in columns, filters, and sorting.
	 */
	query?: QuerySpec | null;
	/**
	 * Selected custom fields and optional $keywords; system columns are accepted and the resource metadata envelope remains present. Omitted returns full values on direct queries and saved columns on views; an empty array returns no custom values. Selected fields need not be indexed. Cursors bind this selection.
	 */
	select?: string[] | null;
	/**
	 * The surface property
	 */
	surface?: QueryRequest_surface | null;
}
export type QueryRequest_surface = (typeof QueryRequest_surfaceObject)[keyof typeof QueryRequest_surfaceObject];
export interface QueryResult extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The search text matched more than 10,000 entries; the page covers only the first 10,000 matches. Narrow the search.
	 */
	searchCapped?: boolean | null;
	/**
	 * The total property
	 */
	total?: number | null;
	/**
	 * More entries match than total_limit; total is the limit.
	 */
	totalCapped?: boolean | null;
	/**
	 * The value property
	 */
	value?: Resource[] | null;
}
/**
 * Collection query over visible surfaces. System fields: $id, $name, $keywords, $created_at, $created_by, $modified_at, $modified_by. Times are RFC3339 UTC and modification metadata follows the selected revision. Archive filters apply before paging, counts, and grouping. $archived_at and $archived_by are system fields usable in columns, filters, and sorting.
 */
export interface QuerySpec extends Parsable {
	/**
	 * Discovery state. Omitted selects active items; archive status is independent of publication and deletion.
	 */
	archive?: ArchiveSelection | null;
	/**
	 * Optional collection content type filter. Field filters, sorting and grouping must use fields assigned to that type.
	 */
	contentTypeId?: Guid | null;
	/**
	 * One condition or AND/OR/NOT group; maximum 32 nodes and depth 6. Custom fields must be indexed. $id/$name are system fields; $keywords supports equality. Multi-valued eq matches any member; ne requires present with no equal member. Collection and smart-folder rules also accept $file_type for the MIME type of the visible file revision.
	 */
	filter?: FilterExpr | null;
	/**
	 * The group_by property
	 */
	groupBy?: string | null;
	/**
	 * Keyword term ID or label. Matches intrinsic keywords and keywords fields on the selected visible surface.
	 */
	keyword?: string | null;
	/**
	 * The parent_id property
	 */
	parentId?: string | null;
	/**
	 * The search property
	 */
	search?: string | null;
	/**
	 * Ordered unique sort keys with independent asc/desc directions. Custom keys require ready scalar indexes. Missing values sort last at each key; final item ID ties follow the primary direction. Omitted or empty uses $id ascending.
	 */
	sort?: SortSpec[] | null;
}
/**
 * How the text is read.
 */
export interface ReadText extends Parsable {
	/**
	 * OCR language codes joined with + (eng+deu); the file's languages when omitted.
	 */
	languages?: string | null;
	/**
	 * Pages read, 500 when omitted; later pages are left out (truncated).
	 */
	maxPages?: number | null;
	/**
	 * auto (the default) reads text layers and OCRs pages and images without one; always OCRs every page; never reads text layers only.
	 */
	ocr?: ReadText_ocr | null;
}
export type ReadText_ocr = (typeof ReadText_ocrObject)[keyof typeof ReadText_ocrObject];
export interface ReclaimedFileVersion extends Parsable {
	/**
	 * The availability property
	 */
	availability?: string | null;
	/**
	 * The blob_id property
	 */
	blobId?: string | null;
	/**
	 * The deferred property
	 */
	deferred?: boolean | null;
	/**
	 * The reason property
	 */
	reason?: string | null;
	/**
	 * The reclaim_after property
	 */
	reclaimAfter?: Date | null;
}
export interface ReclaimFileVersion extends Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: string | null;
}
export interface RecycleBinPage extends AdditionalDataHolder, Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: DeletedResource[] | null;
}
export interface Relationship extends AdditionalDataHolder, Parsable {
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The directed property
	 */
	directed?: boolean | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The metadata property
	 */
	metadata?: Relationship_metadata | null;
	/**
	 * The source_id property
	 */
	sourceId?: string | null;
	/**
	 * The target_id property
	 */
	targetId?: string | null;
	/**
	 * The type_id property
	 */
	typeId?: string | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface Relationship_metadata extends AdditionalDataHolder, Parsable {
}
/**
 * Compact navigation information from the endpoint surface visible to the reader.
 */
export interface RelationshipEndpoint extends AdditionalDataHolder, Parsable {
	/**
	 * The collection_id property
	 */
	collectionId?: Guid | null;
	/**
	 * The collection_name property
	 */
	collectionName?: string | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
	/**
	 * The workspace_name property
	 */
	workspaceName?: string | null;
}
export interface RelationshipEndpointPage extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: RelationshipEndpoint[] | null;
}
export interface RelationshipNeighborhood extends AdditionalDataHolder, Parsable {
	/**
	 * The edges property
	 */
	edges?: WorkspaceRelationshipView[] | null;
	/**
	 * The nodes property
	 */
	nodes?: RelationshipEndpoint[] | null;
	/**
	 * More qualifying connections exist than this neighborhood displays. Use the paginated list or refine filters.
	 */
	truncated?: boolean | null;
}
/**
 * Graph policy whose ID is an organization taxonomy term ID. Vocabulary identity, names, labels and synonyms live in term. Existing policy semantics cannot be changed by ensure.
 */
export interface RelationshipType extends Parsable {
	/**
	 * The attributes property
	 */
	attributes?: RelationshipType_attributes | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The directed property
	 */
	directed?: boolean | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The inverse_label property
	 */
	inverseLabel?: string | null;
	/**
	 * The max_incoming property
	 */
	maxIncoming?: number | null;
	/**
	 * The max_outgoing property
	 */
	maxOutgoing?: number | null;
	/**
	 * The term property
	 */
	term?: Term | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface RelationshipType_attributes extends AdditionalDataHolder, Parsable {
}
export interface RelationshipTypeInput extends Parsable {
	/**
	 * The attributes property
	 */
	attributes?: CreateField[] | null;
	/**
	 * The directed property
	 */
	directed?: boolean | null;
	/**
	 * The inverse_label property
	 */
	inverseLabel?: string | null;
	/**
	 * The max_incoming property
	 */
	maxIncoming?: number | null;
	/**
	 * The max_outgoing property
	 */
	maxOutgoing?: number | null;
	/**
	 * Predicate term ID or label. A new label contributes a term to the open Relationships/Types vocabulary.
	 */
	name?: string | null;
}
export interface RelationshipTypePage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: RelationshipType[] | null;
}
export interface RelationshipView extends Parsable, Relationship {
	/**
	 * The related_item property
	 */
	relatedItem?: LocatedItem | null;
}
export interface Resource extends AdditionalDataHolder, Parsable {
	/**
	 * Lists and libraries only: permits creating physical folders. Disabling retains existing folders. Changes require manage access.
	 */
	allowFolders?: boolean | null;
	/**
	 * The archived property
	 */
	archived?: boolean | null;
	/**
	 * The archived_at property
	 */
	archivedAt?: Date | null;
	/**
	 * The archived_by property
	 */
	archivedBy?: string | null;
	/**
	 * The container_id property
	 */
	containerId?: Guid | null;
	/**
	 * Items and folders: an attached collection content type. Creation selects the default when omitted. PATCH changes type, keeps shared fields and removes fields absent from the destination type. Folder required fields and creation defaults do not apply.
	 */
	contentTypeId?: Guid | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * Creation actor or selected content revision modification actor.
	 */
	createdBy?: string | null;
	/**
	 * Workspace only. Empty inherits the organization default. Changing this affects newly created currency fields, not existing definitions.
	 */
	defaultCurrency?: string | null;
	/**
	 * Workspace or collection description, limited to 2000 UTF-16 code units. Changes require manage access.
	 */
	description?: string | null;
	/**
	 * Libraries only. Default warn. Compares hashes against current documents visible to the caller across libraries and workspaces. Allow suppresses warnings; block rejects duplicates with 409 duplicate_file.
	 */
	duplicatePolicy?: Resource_duplicate_policy | null;
	/**
	 * Omitted from published read surfaces.
	 */
	headRevisionId?: Guid | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The inherit_permissions property
	 */
	inheritPermissions?: boolean | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * The kind property
	 */
	kind?: Resource_kind | null;
	/**
	 * History entries kept per item, trimmed on each new recorded version. Defaults to 50.
	 */
	maxVersions?: number | null;
	/**
	 * Item and folder titles contain at most 1024 UTF-16 units before trimming. Workspace and collection names allow 200 units. Library names also exclude path separators and control characters and are unique among live siblings ignoring case.
	 */
	name?: string | null;
	/**
	 * Internal content counter; omitted from published read surfaces.
	 */
	nextRevisionNumber?: number | null;
	/**
	 * The parent_id property
	 */
	parentId?: Guid | null;
	/**
	 * Owner of a personal Home workspace; membership is fixed.
	 */
	personalOwnerId?: string | null;
	/**
	 * The published_revision_id property
	 */
	publishedRevisionId?: Guid | null;
	/**
	 * Lists/libraries only. False publishes content changes immediately. Changing policy requires manage access; disabling publishes all current heads atomically.
	 */
	publishingEnabled?: boolean | null;
	/**
	 * The schema_head_id property
	 */
	schemaHeadId?: Guid | null;
	/**
	 * Protected default Home library identity.
	 */
	systemKey?: Resource_system_key | null;
	/**
	 * Template keys adopted into a collection; used by smart folder selectors.
	 */
	templateKeys?: string[] | null;
	/**
	 * For items, creation timestamp of the selected content revision.
	 */
	updatedAt?: Date | null;
	/**
	 * Creation actor or selected content revision modification actor.
	 */
	updatedBy?: string | null;
	/**
	 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
	 */
	values?: Resource_values | null;
	/**
	 * Optimistic resource lock version, independent of content revision_number and blob version.
	 */
	version?: number | null;
	/**
	 * Collections only. Lists and libraries default to off. Off stops recording new history; retained entries remain available. Replacing a file while off queues its previous bytes for reclamation after the one-hour grace period, subject to current/published file, staged-use and workflow-lease protection.
	 */
	versioning?: Resource_versioning | null;
	/**
	 * Libraries only. Serves the library's folders and files at /webdav/{id}/.
	 */
	webdavEnabled?: boolean | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
export type Resource_duplicate_policy = (typeof Resource_duplicate_policyObject)[keyof typeof Resource_duplicate_policyObject];
export type Resource_kind = (typeof Resource_kindObject)[keyof typeof Resource_kindObject];
export type Resource_system_key = (typeof Resource_system_keyObject)[keyof typeof Resource_system_keyObject];
/**
 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
 */
export interface Resource_values extends AdditionalDataHolder, Parsable {
}
export type Resource_versioning = (typeof Resource_versioningObject)[keyof typeof Resource_versioningObject];
/**
 * A resource with the actions the caller may take on it. Clients use it to offer only allowed actions; the server still checks every request.
 */
export interface ResourceAccess extends Parsable, Resource {
	/**
	 * Allowed actions, in the order read, read_draft, write, publish, manage. Workspace ACLs and the caller's module scopes both apply: write and publish below a workspace also need list.write. On a workspace, manage means owner; on a list or library, write means adding and editing content and manage means changing settings, fields, views and permissions.
	 */
	access?: ResourceAccess_access[] | null;
}
export type ResourceAccess_access = (typeof ResourceAccess_accessObject)[keyof typeof ResourceAccess_accessObject];
export interface SchemaRevision extends AdditionalDataHolder, Parsable {
	/**
	 * The container_id property
	 */
	containerId?: Guid | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The created_by property
	 */
	createdBy?: string | null;
	/**
	 * Frozen resolved collection fields with catalog definition_id and content_types with ID, key, name, field_keys, rules and collection default. Historical revisions retain this definition after live catalog updates.
	 */
	definition?: SchemaRevision_definition | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The revision_number property
	 */
	revisionNumber?: number | null;
}
/**
 * Frozen resolved collection fields with catalog definition_id and content_types with ID, key, name, field_keys, rules and collection default. Historical revisions retain this definition after live catalog updates.
 */
export interface SchemaRevision_definition extends AdditionalDataHolder, Parsable {
	/**
	 * The fields property
	 */
	fields?: SchemaRevision_definition_fields[] | null;
}
export interface SchemaRevision_definition_fields extends AdditionalDataHolder, Parsable {
	/**
	 * The choices property
	 */
	choices?: string[] | null;
	/**
	 * Immutable field key.
	 */
	id?: string | null;
	/**
	 * The indexed property
	 */
	indexed?: boolean | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The required property
	 */
	required?: boolean | null;
	/**
	 * Immutable decimal scale; zero for all other field types. Scaled units must fit signed 64 bits.
	 */
	scale?: number | null;
	/**
	 * The type property
	 */
	type?: SchemaRevision_definition_fields_type | null;
}
export type SchemaRevision_definition_fields_type = (typeof SchemaRevision_definition_fields_typeObject)[keyof typeof SchemaRevision_definition_fields_typeObject];
export interface SchemaTemplate extends Parsable {
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The definition property
	 */
	definition?: SchemaTemplate_definition | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: string | null;
}
export interface SchemaTemplate_definition extends AdditionalDataHolder, Parsable {
}
export interface SchemaTemplatePage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: SchemaTemplate[] | null;
}
/**
 * Counts of the matches the store holds per value, before the recheck of current permissions; the most frequent values of each.
 */
export interface SearchFacets extends Parsable {
	/**
	 * The collection property
	 */
	collection?: SearchFacetValue[] | null;
	/**
	 * The content_type property
	 */
	contentType?: SearchFacetValue[] | null;
	/**
	 * Keyword term ID or label. Matches intrinsic keywords and keywords fields on the selected visible surface.
	 */
	keyword?: SearchFacetValue[] | null;
	/**
	 * The term property
	 */
	term?: SearchFacetValue[] | null;
	/**
	 * The workspace property
	 */
	workspace?: SearchFacetValue[] | null;
}
export interface SearchFacetValue extends Parsable {
	/**
	 * The count property
	 */
	count?: number | null;
	/**
	 * The value property
	 */
	value?: string | null;
}
export interface SearchField extends Parsable {
	/**
	 * The choices property
	 */
	choices?: string[] | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The lookup_container_id property
	 */
	lookupContainerId?: Guid | null;
	/**
	 * The multiple property
	 */
	multiple?: boolean | null;
	/**
	 * The operators property
	 */
	operators?: SearchField_operators[] | null;
	/**
	 * True when the field has a ready index for collection and smart-folder rules.
	 */
	queryable?: boolean | null;
	/**
	 * The scale property
	 */
	scale?: number | null;
	/**
	 * The term_set_id property
	 */
	termSetId?: string | null;
	/**
	 * The type property
	 */
	type?: string | null;
}
export type SearchField_operators = (typeof SearchField_operatorsObject)[keyof typeof SearchField_operatorsObject];
export interface SearchHit extends Parsable {
	/**
	 * The archived property
	 */
	archived?: boolean | null;
	/**
	 * The archived_at property
	 */
	archivedAt?: Date | null;
	/**
	 * The archived_by property
	 */
	archivedBy?: string | null;
	/**
	 * The collection_id property
	 */
	collectionId?: Guid | null;
	/**
	 * The content_type_id property
	 */
	contentTypeId?: string | null;
	/**
	 * Byte ranges of matches in snippet.
	 */
	highlights?: UntypedNode | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The kind property
	 */
	kind?: SearchHit_kind | null;
	/**
	 * How the hit was found: by its words, by meaning, or both; empty when no words were searched.
	 */
	matchedBy?: SearchHit_matched_by[] | null;
	/**
	 * The modified_at property
	 */
	modifiedAt?: Date | null;
	/**
	 * The modified_by property
	 */
	modifiedBy?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The page of the file the snippet is on, when the file's text matched.
	 */
	page?: number | null;
	/**
	 * The rank property
	 */
	rank?: number | null;
	/**
	 * Body text around the first match.
	 */
	snippet?: string | null;
	/**
	 * The surface property
	 */
	surface?: SearchHit_surface | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
export type SearchHit_kind = (typeof SearchHit_kindObject)[keyof typeof SearchHit_kindObject];
export type SearchHit_matched_by = (typeof SearchHit_matched_byObject)[keyof typeof SearchHit_matched_byObject];
export type SearchHit_surface = (typeof SearchHit_surfaceObject)[keyof typeof SearchHit_surfaceObject];
export interface SearchPage extends Parsable {
	/**
	 * Counts from matching sources verified against current permissions. Omitted if any source is no longer visible.
	 */
	facets?: SearchFacets | null;
	/**
	 * The mode the search ran in: keyword when embedding the query failed.
	 */
	mode?: SearchPage_mode | null;
	/**
	 * Absolute continuation URL, or null on the final page.
	 */
	odataNextLink?: string | null;
	/**
	 * Matches in the store, before the recheck of current permissions.
	 */
	total?: number | null;
	/**
	 * The native candidate limit was reached or entries failed the current permission recheck; total and facets may be estimates.
	 */
	totalEstimated?: boolean | null;
	/**
	 * The value property
	 */
	value?: SearchHit[] | null;
}
export type SearchPage_mode = (typeof SearchPage_modeObject)[keyof typeof SearchPage_modeObject];
export interface SearchRequest extends Parsable {
	/**
	 * Discovery state. Omitted selects active items; archive status is independent of publication and deletion.
	 */
	archive?: ArchiveSelection | null;
	/**
	 * The archived_by property
	 */
	archivedBy?: string | null;
	/**
	 * The archived_from property
	 */
	archivedFrom?: Date | null;
	/**
	 * The archived_to property
	 */
	archivedTo?: Date | null;
	/**
	 * The collection_id property
	 */
	collectionId?: string | null;
	/**
	 * The content_type_id property
	 */
	contentTypeId?: string | null;
	/**
	 * The created_by property
	 */
	createdBy?: string | null;
	/**
	 * The facets property
	 */
	facets?: boolean | null;
	/**
	 * Filter on search fields (GET /v1/search/fields): at most 32 nodes and depth 6. Unknown fields, unsupported operators and mistyped values answer 422.
	 */
	filter?: FilterExpr | null;
	/**
	 * Keyword term ID or label. Matches intrinsic keywords and keywords fields on the selected visible surface.
	 */
	keyword?: string | null;
	/**
	 * The kind property
	 */
	kind?: SearchRequest_kind[] | null;
	/**
	 * keyword matches words; semantic finds file content close in meaning (an embedding model is required); hybrid fuses both by reciprocal rank. Default hybrid when an embedding model is configured and q is given, else keyword.
	 */
	mode?: SearchRequest_mode | null;
	/**
	 * The modified_by property
	 */
	modifiedBy?: string | null;
	/**
	 * The modified_from property
	 */
	modifiedFrom?: Date | null;
	/**
	 * The modified_to property
	 */
	modifiedTo?: Date | null;
	/**
	 * The q property
	 */
	q?: string | null;
	/**
	 * The surface property
	 */
	surface?: SearchRequest_surface | null;
	/**
	 * The term_id property
	 */
	termId?: string | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: string | null;
}
export type SearchRequest_kind = (typeof SearchRequest_kindObject)[keyof typeof SearchRequest_kindObject];
export type SearchRequest_mode = (typeof SearchRequest_modeObject)[keyof typeof SearchRequest_modeObject];
export type SearchRequest_surface = (typeof SearchRequest_surfaceObject)[keyof typeof SearchRequest_surfaceObject];
export interface SearchStatus extends Parsable {
	/**
	 * The bytes property
	 */
	bytes?: number | null;
	/**
	 * Published chunks of file content.
	 */
	chunks?: number | null;
	/**
	 * The documents property
	 */
	documents?: number | null;
	/**
	 * The embeddings property
	 */
	embeddings?: SearchStatus_embeddings | null;
	/**
	 * The embedding model's vector space, e.g. openai:text-embedding-3-small:1536; absent without a model or while it is unavailable.
	 */
	model?: string | null;
	/**
	 * A rebuild of a new store is running; search answers 503 until it completes.
	 */
	rebuilding?: boolean | null;
	/**
	 * The configured search store: sqlite or zvec.
	 */
	store?: string | null;
}
export interface SearchStatus_embeddings extends Parsable {
	/**
	 * Cached normalized vector bytes for the model; native index and source-catalog overhead are additional.
	 */
	memoryBytes?: number | null;
	/**
	 * Published chunk inputs without a vector of the model, which the embedding sweep embeds.
	 */
	pending?: number | null;
	/**
	 * Stored vectors of the model.
	 */
	vectors?: number | null;
}
export interface SearchSync extends Parsable {
	/**
	 * The resource_ids property
	 */
	resourceIds?: string[] | null;
}
declare function serializeApplyTemplateInput(writer: SerializationWriter, applyTemplateInput?: Partial<ApplyTemplateInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeArchiveEntry(writer: SerializationWriter, archiveEntry?: Partial<ArchiveEntry> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeArchiveEntry_values(writer: SerializationWriter, archiveEntry_values?: Partial<ArchiveEntry_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeArtifact(writer: SerializationWriter, artifact?: Partial<Artifact> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeArtifact_metrics(writer: SerializationWriter, artifact_metrics?: Partial<Artifact_metrics> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeArtifact_sources(writer: SerializationWriter, artifact_sources?: Partial<Artifact_sources> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeAttachContentTypeInput(writer: SerializationWriter, attachContentTypeInput?: Partial<AttachContentTypeInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeAuditEvent(writer: SerializationWriter, auditEvent?: Partial<AuditEvent> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeAuditEvent_details(writer: SerializationWriter, auditEvent_details?: Partial<AuditEvent_details> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeAuditPage(writer: SerializationWriter, auditPage?: Partial<AuditPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBlob(writer: SerializationWriter, blob?: Partial<Blob> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkCreate(writer: SerializationWriter, bulkCreate?: Partial<BulkCreate> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkCreate_values(writer: SerializationWriter, bulkCreate_values?: Partial<BulkCreate_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperation(writer: SerializationWriter, bulkOperation?: Partial<BulkOperationMember1 | BulkOperationMember2 | BulkOperationMember3 | BulkOperationMember4 | BulkOperationMember5 | BulkOperationMember6 | BulkOperationMember7> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperationMember1(writer: SerializationWriter, bulkOperationMember1?: Partial<BulkOperationMember1> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperationMember2(writer: SerializationWriter, bulkOperationMember2?: Partial<BulkOperationMember2> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperationMember3(writer: SerializationWriter, bulkOperationMember3?: Partial<BulkOperationMember3> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperationMember4(writer: SerializationWriter, bulkOperationMember4?: Partial<BulkOperationMember4> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperationMember5(writer: SerializationWriter, bulkOperationMember5?: Partial<BulkOperationMember5> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperationMember6(writer: SerializationWriter, bulkOperationMember6?: Partial<BulkOperationMember6> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkOperationMember7(writer: SerializationWriter, bulkOperationMember7?: Partial<BulkOperationMember7> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkRequest(writer: SerializationWriter, bulkRequest?: Partial<BulkRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkResponse(writer: SerializationWriter, bulkResponse?: Partial<BulkResponse> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkResult(writer: SerializationWriter, bulkResult?: Partial<BulkResult> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkUpdate(writer: SerializationWriter, bulkUpdate?: Partial<BulkUpdate> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeBulkUpdate_values(writer: SerializationWriter, bulkUpdate_values?: Partial<BulkUpdate_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCalendarOccurrence(writer: SerializationWriter, calendarOccurrence?: Partial<CalendarOccurrence> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCalendarQuery(writer: SerializationWriter, calendarQuery?: Partial<CalendarQuery> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeChunkText(writer: SerializationWriter, chunkText?: Partial<ChunkText> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCollectionContentType(writer: SerializationWriter, collectionContentType?: Partial<CollectionContentType> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCollectionTemplate(writer: SerializationWriter, collectionTemplate?: Partial<CollectionTemplate> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCompletionRequest(writer: SerializationWriter, completionRequest?: Partial<CompletionRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCompletionRequest_output_schema(writer: SerializationWriter, completionRequest_output_schema?: Partial<CompletionRequest_output_schema> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeComposePDF(writer: SerializationWriter, composePDF?: Partial<ComposePDF> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeContentType(writer: SerializationWriter, contentType?: Partial<ContentType> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeContentTypeInput(writer: SerializationWriter, contentTypeInput?: Partial<ContentTypeInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateCatalogField(writer: SerializationWriter, createCatalogField?: Partial<CreateCatalogField> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateField(writer: SerializationWriter, createField?: Partial<CreateField> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateListIntegration(writer: SerializationWriter, createListIntegration?: Partial<CreateListIntegration> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateListIntegration_configuration(writer: SerializationWriter, createListIntegration_configuration?: Partial<CreateListIntegration_configuration> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateRelationship(writer: SerializationWriter, createRelationship?: Partial<CreateRelationship> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateRelationship_metadata(writer: SerializationWriter, createRelationship_metadata?: Partial<CreateRelationship_metadata> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateResource(writer: SerializationWriter, createResource?: Partial<CreateResource> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeCreateResource_values(writer: SerializationWriter, createResource_values?: Partial<CreateResource_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDecisionRequest(writer: SerializationWriter, decisionRequest?: Partial<DecisionRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDecisionRequest_context(writer: SerializationWriter, decisionRequest_context?: Partial<DecisionRequest_context> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDecisionRequest_data(writer: SerializationWriter, decisionRequest_data?: Partial<DecisionRequest_data> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDecisionRequest_questions(writer: SerializationWriter, decisionRequest_questions?: Partial<DecisionRequest_questions> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDecisionRequest_questions_options(writer: SerializationWriter, decisionRequest_questions_options?: Partial<DecisionRequest_questions_options> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDeletedResource(writer: SerializationWriter, deletedResource?: Partial<DeletedResource> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDeltaPage(writer: SerializationWriter, deltaPage?: Partial<DeltaPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDeltaPage_value(writer: SerializationWriter, deltaPage_value?: Partial<DeltaPage_valueMember1 | Resource> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDeltaPage_valueMember1(writer: SerializationWriter, deltaPage_valueMember1?: Partial<DeltaPage_valueMember1> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDeltaPage_valueMember1_Removed(writer: SerializationWriter, deltaPage_valueMember1_Removed?: Partial<DeltaPage_valueMember1_Removed> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDocumentMetadata(writer: SerializationWriter, documentMetadata?: Partial<DocumentMetadata> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDocumentMetadata_values(writer: SerializationWriter, documentMetadata_values?: Partial<DocumentMetadata_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDocumentUpload(writer: SerializationWriter, documentUpload?: Partial<DocumentUpload> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeDuplicateDocument(writer: SerializationWriter, duplicateDocument?: Partial<DuplicateDocument> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeFieldDefinition(writer: SerializationWriter, fieldDefinition?: Partial<FieldDefinition> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeFieldOptions(writer: SerializationWriter, fieldOptions?: Partial<FieldOptions> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeFileVersion(writer: SerializationWriter, fileVersion?: Partial<FileVersion> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeFilteredBulkUpdate(writer: SerializationWriter, filteredBulkUpdate?: Partial<FilteredBulkUpdate> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeFilteredBulkUpdate_values(writer: SerializationWriter, filteredBulkUpdate_values?: Partial<FilteredBulkUpdate_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeFilterExpr(writer: SerializationWriter, filterExpr?: Partial<FilterExpr> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeGenerationChunk(writer: SerializationWriter, generationChunk?: Partial<GenerationChunk> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeGenerationChunkPage(writer: SerializationWriter, generationChunkPage?: Partial<GenerationChunkPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeGenerationEnrichments(writer: SerializationWriter, generationEnrichments?: Partial<GenerationEnrichments> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeGenerationEnrichments_enrichments(writer: SerializationWriter, generationEnrichments_enrichments?: Partial<GenerationEnrichments_enrichments> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeItemIntegration(writer: SerializationWriter, itemIntegration?: Partial<ItemIntegration> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeItemRevision(writer: SerializationWriter, itemRevision?: Partial<ItemRevision> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeItemRevision_payload(writer: SerializationWriter, itemRevision_payload?: Partial<ItemRevision_payload> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeItemText(writer: SerializationWriter, itemText?: Partial<ItemText> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeItemText_pages(writer: SerializationWriter, itemText_pages?: Partial<ItemText_pages> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeKeywordCount(writer: SerializationWriter, keywordCount?: Partial<KeywordCount> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeKeywordCountList(writer: SerializationWriter, keywordCountList?: Partial<KeywordCountList> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeKeywordInput(writer: SerializationWriter, keywordInput?: Partial<KeywordInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeListIntegration(writer: SerializationWriter, listIntegration?: Partial<ListIntegration> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeListView(writer: SerializationWriter, listView?: Partial<ListView> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeListViewPage(writer: SerializationWriter, listViewPage?: Partial<ListViewPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeLocatedItem(writer: SerializationWriter, locatedItem?: Partial<LocatedItem> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeMergeTerm(writer: SerializationWriter, mergeTerm?: Partial<MergeTerm> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeMoveItem(writer: SerializationWriter, moveItem?: Partial<MoveItem> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeMoveTerm(writer: SerializationWriter, moveTerm?: Partial<MoveTerm> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeOperation(writer: SerializationWriter, operation?: Partial<Operation> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeOperationFailure(writer: SerializationWriter, operationFailure?: Partial<OperationFailure> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeOperationPage(writer: SerializationWriter, operationPage?: Partial<OperationPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeOptimizeImage(writer: SerializationWriter, optimizeImage?: Partial<OptimizeImage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePermissions(writer: SerializationWriter, permissions?: Partial<Permissions> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePermissions_effective_grants(writer: SerializationWriter, permissions_effective_grants?: Partial<Permissions_effective_grants> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePermissions_grants(writer: SerializationWriter, permissions_grants?: Partial<Permissions_grants> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePortableSmartFolder(writer: SerializationWriter, portableSmartFolder?: Partial<PortableSmartFolder> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePortableSmartFolderDefinition(writer: SerializationWriter, portableSmartFolderDefinition?: Partial<PortableSmartFolderDefinition> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePortableTerm(writer: SerializationWriter, portableTerm?: Partial<PortableTerm> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePortableTerm_labels(writer: SerializationWriter, portableTerm_labels?: Partial<PortableTerm_labels> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePortableTermGroup(writer: SerializationWriter, portableTermGroup?: Partial<PortableTermGroup> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePortableTermSet(writer: SerializationWriter, portableTermSet?: Partial<PortableTermSet> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeProblem(writer: SerializationWriter, problem?: Partial<Problem> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeProblem_existing_item(writer: SerializationWriter, problem_existing_item?: Partial<Problem_existing_item> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePromotedFile(writer: SerializationWriter, promotedFile?: Partial<PromotedFile> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePromoteKeyword(writer: SerializationWriter, promoteKeyword?: Partial<PromoteKeyword> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePromoteStagedFile(writer: SerializationWriter, promoteStagedFile?: Partial<PromoteStagedFile> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePublication(writer: SerializationWriter, publication?: Partial<Publication> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializePublication_snapshot(writer: SerializationWriter, publication_snapshot?: Partial<Publication_snapshot> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeQueryGroup(writer: SerializationWriter, queryGroup?: Partial<QueryGroup> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeQueryGroupPage(writer: SerializationWriter, queryGroupPage?: Partial<QueryGroupPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeQueryRequest(writer: SerializationWriter, queryRequest?: Partial<QueryRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeQueryResult(writer: SerializationWriter, queryResult?: Partial<QueryResult> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeQuerySpec(writer: SerializationWriter, querySpec?: Partial<QuerySpec> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeReadText(writer: SerializationWriter, readText?: Partial<ReadText> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeReclaimedFileVersion(writer: SerializationWriter, reclaimedFileVersion?: Partial<ReclaimedFileVersion> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeReclaimFileVersion(writer: SerializationWriter, reclaimFileVersion?: Partial<ReclaimFileVersion> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRecycleBinPage(writer: SerializationWriter, recycleBinPage?: Partial<RecycleBinPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationship(writer: SerializationWriter, relationship?: Partial<Relationship> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationship_metadata(writer: SerializationWriter, relationship_metadata?: Partial<Relationship_metadata> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipEndpoint(writer: SerializationWriter, relationshipEndpoint?: Partial<RelationshipEndpoint> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipEndpointPage(writer: SerializationWriter, relationshipEndpointPage?: Partial<RelationshipEndpointPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipNeighborhood(writer: SerializationWriter, relationshipNeighborhood?: Partial<RelationshipNeighborhood> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipType(writer: SerializationWriter, relationshipType?: Partial<RelationshipType> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipType_attributes(writer: SerializationWriter, relationshipType_attributes?: Partial<RelationshipType_attributes> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipTypeInput(writer: SerializationWriter, relationshipTypeInput?: Partial<RelationshipTypeInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipTypePage(writer: SerializationWriter, relationshipTypePage?: Partial<RelationshipTypePage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeRelationshipView(writer: SerializationWriter, relationshipView?: Partial<RelationshipView> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeResource(writer: SerializationWriter, resource?: Partial<Resource> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeResource_values(writer: SerializationWriter, resource_values?: Partial<Resource_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeResourceAccess(writer: SerializationWriter, resourceAccess?: Partial<ResourceAccess> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSchemaRevision(writer: SerializationWriter, schemaRevision?: Partial<SchemaRevision> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSchemaRevision_definition(writer: SerializationWriter, schemaRevision_definition?: Partial<SchemaRevision_definition> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSchemaRevision_definition_fields(writer: SerializationWriter, schemaRevision_definition_fields?: Partial<SchemaRevision_definition_fields> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSchemaTemplate(writer: SerializationWriter, schemaTemplate?: Partial<SchemaTemplate> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSchemaTemplate_definition(writer: SerializationWriter, schemaTemplate_definition?: Partial<SchemaTemplate_definition> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSchemaTemplatePage(writer: SerializationWriter, schemaTemplatePage?: Partial<SchemaTemplatePage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchFacets(writer: SerializationWriter, searchFacets?: Partial<SearchFacets> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchFacetValue(writer: SerializationWriter, searchFacetValue?: Partial<SearchFacetValue> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchField(writer: SerializationWriter, searchField?: Partial<SearchField> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchHit(writer: SerializationWriter, searchHit?: Partial<SearchHit> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchPage(writer: SerializationWriter, searchPage?: Partial<SearchPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchRequest(writer: SerializationWriter, searchRequest?: Partial<SearchRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchStatus(writer: SerializationWriter, searchStatus?: Partial<SearchStatus> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchStatus_embeddings(writer: SerializationWriter, searchStatus_embeddings?: Partial<SearchStatus_embeddings> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSearchSync(writer: SerializationWriter, searchSync?: Partial<SearchSync> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSetWorkspaceMember(writer: SerializationWriter, setWorkspaceMember?: Partial<SetWorkspaceMember> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolder(writer: SerializationWriter, smartFolder?: Partial<SmartFolder> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderDefinition(writer: SerializationWriter, smartFolderDefinition?: Partial<SmartFolderDefinition> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderDrop(writer: SerializationWriter, smartFolderDrop?: Partial<SmartFolderDrop> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderEntry(writer: SerializationWriter, smartFolderEntry?: Partial<SmartFolderEntry> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderGroup(writer: SerializationWriter, smartFolderGroup?: Partial<SmartFolderGroup> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderGroupBy(writer: SerializationWriter, smartFolderGroupBy?: Partial<SmartFolderGroupBy> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderGroupsResult(writer: SerializationWriter, smartFolderGroupsResult?: Partial<SmartFolderGroupsResult> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderImportResult(writer: SerializationWriter, smartFolderImportResult?: Partial<SmartFolderImportResult> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderInput(writer: SerializationWriter, smartFolderInput?: Partial<SmartFolderInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderPackage(writer: SerializationWriter, smartFolderPackage?: Partial<SmartFolderPackage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderPage(writer: SerializationWriter, smartFolderPage?: Partial<SmartFolderPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderPreview(writer: SerializationWriter, smartFolderPreview?: Partial<SmartFolderPreview> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderQueryRequest(writer: SerializationWriter, smartFolderQueryRequest?: Partial<SmartFolderQueryRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderQueryRequest_sort(writer: SerializationWriter, smartFolderQueryRequest_sort?: Partial<SmartFolderQueryRequest_sort> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderResult(writer: SerializationWriter, smartFolderResult?: Partial<SmartFolderResult> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderTermRef(writer: SerializationWriter, smartFolderTermRef?: Partial<SmartFolderTermRef> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSmartFolderUnclassify(writer: SerializationWriter, smartFolderUnclassify?: Partial<SmartFolderUnclassify> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeSortSpec(writer: SerializationWriter, sortSpec?: Partial<SortSpec> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeStagedArtifact(writer: SerializationWriter, stagedArtifact?: Partial<StagedArtifact> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeStagedFile(writer: SerializationWriter, stagedFile?: Partial<StagedFile> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeStagedFile_metrics(writer: SerializationWriter, stagedFile_metrics?: Partial<StagedFile_metrics> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeStagedFile_sources(writer: SerializationWriter, stagedFile_sources?: Partial<StagedFile_sources> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTask(writer: SerializationWriter, task?: Partial<Task> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTask_input(writer: SerializationWriter, task_input?: Partial<Task_input> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTask_review(writer: SerializationWriter, task_review?: Partial<Task_review> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTask_values(writer: SerializationWriter, task_values?: Partial<Task_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskAnswer(writer: SerializationWriter, taskAnswer?: Partial<TaskAnswer> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskAnswer_input(writer: SerializationWriter, taskAnswer_input?: Partial<TaskAnswer_input> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskCreate(writer: SerializationWriter, taskCreate?: Partial<TaskCreate> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskCreate_review(writer: SerializationWriter, taskCreate_review?: Partial<TaskCreate_review> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskCreate_values(writer: SerializationWriter, taskCreate_values?: Partial<TaskCreate_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskList(writer: SerializationWriter, taskList?: Partial<TaskList> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskOutcome(writer: SerializationWriter, taskOutcome?: Partial<TaskOutcome> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskReassign(writer: SerializationWriter, taskReassign?: Partial<TaskReassign> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskResponseInput(writer: SerializationWriter, taskResponseInput?: Partial<TaskResponseInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaskResponseInput_input(writer: SerializationWriter, taskResponseInput_input?: Partial<TaskResponseInput_input> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaxonomyDecisionRequest(writer: SerializationWriter, taxonomyDecisionRequest?: Partial<TaxonomyDecisionRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaxonomyDecisionRequest_context(writer: SerializationWriter, taxonomyDecisionRequest_context?: Partial<TaxonomyDecisionRequest_context> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaxonomyDecisionRequest_data(writer: SerializationWriter, taxonomyDecisionRequest_data?: Partial<TaxonomyDecisionRequest_data> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaxonomyDecisionRequest_description_overrides(writer: SerializationWriter, taxonomyDecisionRequest_description_overrides?: Partial<TaxonomyDecisionRequest_description_overrides> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaxonomyImportResult(writer: SerializationWriter, taxonomyImportResult?: Partial<TaxonomyImportResult> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTaxonomyPackage(writer: SerializationWriter, taxonomyPackage?: Partial<TaxonomyPackage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTemplateField(writer: SerializationWriter, templateField?: Partial<TemplateField> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTemplateInput(writer: SerializationWriter, templateInput?: Partial<TemplateInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTerm(writer: SerializationWriter, term?: Partial<Term> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTerm_labels(writer: SerializationWriter, term_labels?: Partial<Term_labels> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermEntries(writer: SerializationWriter, termEntries?: Partial<TermEntries> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermGroup(writer: SerializationWriter, termGroup?: Partial<TermGroup> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermGroupInput(writer: SerializationWriter, termGroupInput?: Partial<TermGroupInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermGroupPage(writer: SerializationWriter, termGroupPage?: Partial<TermGroupPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermImportResult(writer: SerializationWriter, termImportResult?: Partial<TermImportResult> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermInput(writer: SerializationWriter, termInput?: Partial<TermInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermInput_labels(writer: SerializationWriter, termInput_labels?: Partial<TermInput_labels> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermList(writer: SerializationWriter, termList?: Partial<TermList> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermPage(writer: SerializationWriter, termPage?: Partial<TermPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermSet(writer: SerializationWriter, termSet?: Partial<TermSet> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermSetInput(writer: SerializationWriter, termSetInput?: Partial<TermSetInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeTermSetPage(writer: SerializationWriter, termSetPage?: Partial<TermSetPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeUpdateField(writer: SerializationWriter, updateField?: Partial<UpdateField> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeUpdateListIntegration(writer: SerializationWriter, updateListIntegration?: Partial<UpdateListIntegration> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeUpdateListIntegration_configuration(writer: SerializationWriter, updateListIntegration_configuration?: Partial<UpdateListIntegration_configuration> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeUpdateRelationship(writer: SerializationWriter, updateRelationship?: Partial<UpdateRelationship> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeUpdateRelationship_metadata(writer: SerializationWriter, updateRelationship_metadata?: Partial<UpdateRelationship_metadata> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeUpdateResource(writer: SerializationWriter, updateResource?: Partial<UpdateResource> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeUpdateResource_values(writer: SerializationWriter, updateResource_values?: Partial<UpdateResource_values> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeValidationRule(writer: SerializationWriter, validationRule?: Partial<ValidationRuleMember1 | ValidationRuleMember2> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeValidationRuleMember1(writer: SerializationWriter, validationRuleMember1?: Partial<ValidationRuleMember1> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeValidationRuleMember2(writer: SerializationWriter, validationRuleMember2?: Partial<ValidationRuleMember2> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeViewInput(writer: SerializationWriter, viewInput?: Partial<ViewInput> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeViewQueryRequest(writer: SerializationWriter, viewQueryRequest?: Partial<ViewQueryRequest> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkflowForm(writer: SerializationWriter, workflowForm?: Partial<WorkflowForm> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkspaceKeywordCount(writer: SerializationWriter, workspaceKeywordCount?: Partial<WorkspaceKeywordCount> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkspaceKeywordCountPage(writer: SerializationWriter, workspaceKeywordCountPage?: Partial<WorkspaceKeywordCountPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkspaceMember(writer: SerializationWriter, workspaceMember?: Partial<WorkspaceMember> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkspaceRelationshipPage(writer: SerializationWriter, workspaceRelationshipPage?: Partial<WorkspaceRelationshipPage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkspaceRelationshipType(writer: SerializationWriter, workspaceRelationshipType?: Partial<WorkspaceRelationshipType> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkspaceRelationshipTypePage(writer: SerializationWriter, workspaceRelationshipTypePage?: Partial<WorkspaceRelationshipTypePage> | undefined | null, isSerializingDerivedType?: boolean): void;
declare function serializeWorkspaceRelationshipView(writer: SerializationWriter, workspaceRelationshipView?: Partial<WorkspaceRelationshipView> | undefined | null, isSerializingDerivedType?: boolean): void;
export interface SetWorkspaceMember extends Parsable {
	/**
	 * Owner manages the workspace and all descendants; member contributes; visitor reads published content.
	 */
	role?: SetWorkspaceMember_role | null;
	/**
	 * Active organization directory subject. Inactive users cannot be added or promoted. Verified bearer or trusted-proxy subjects enroll on first authentication; user managers can provision subjects through POST /v1/identity/users.
	 */
	subject?: string | null;
}
export type SetWorkspaceMember_role = (typeof SetWorkspaceMember_roleObject)[keyof typeof SetWorkspaceMember_roleObject];
export interface SmartFolder extends AdditionalDataHolder, Parsable {
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The created_by property
	 */
	createdBy?: string | null;
	/**
	 * Live query across at most 100 readable collections. Name/key selectors ignore case. Terms include descendants and can occupy different term fields. Incompatible collection filters are skipped; none compatible returns validation. Metadata navigation is scalar and indexed, with compatible field types/scopes across collections. Physical folders have content types and optional custom fields, and participate in taxonomy/content-type selectors when include_folders is true. Equivalent collection predicates share one SQL branch; distinct shapes use UNION ALL. Queries are bounded to 30,000 SQL parameters. Mode is explicit. Results can be read directly without supplying a group path. $file_type filters the MIME type on the authorized visible revision.
	 */
	definition?: SmartFolderDefinition | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The owner_id property
	 */
	ownerId?: string | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The updated_by property
	 */
	updatedBy?: string | null;
	/**
	 * The version property
	 */
	version?: number | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
/**
 * Live query across at most 100 readable collections. Name/key selectors ignore case. Terms include descendants and can occupy different term fields. Incompatible collection filters are skipped; none compatible returns validation. Metadata navigation is scalar and indexed, with compatible field types/scopes across collections. Physical folders have content types and optional custom fields, and participate in taxonomy/content-type selectors when include_folders is true. Equivalent collection predicates share one SQL branch; distinct shapes use UNION ALL. Queries are bounded to 30,000 SQL parameters. Mode is explicit. Results can be read directly without supplying a group path. $file_type filters the MIME type on the authorized visible revision.
 */
export interface SmartFolderDefinition extends Parsable {
	/**
	 * Discovery state. Omitted selects active items; archive status is independent of publication and deletion.
	 */
	archive?: ArchiveSelection | null;
	/**
	 * Omit to search both lists and libraries.
	 */
	collectionKind?: SmartFolderDefinition_collection_kind | null;
	/**
	 * The collections property
	 */
	collections?: string[] | null;
	/**
	 * The content_types property
	 */
	contentTypes?: string[] | null;
	/**
	 * One condition or AND/OR/NOT group; maximum 32 nodes and depth 6. Custom fields must be indexed. $id/$name are system fields; $keywords supports equality. Multi-valued eq matches any member; ne requires present with no equal member. Collection and smart-folder rules also accept $file_type for the MIME type of the visible file revision.
	 */
	filter?: FilterExpr | null;
	/**
	 * The group_by property
	 */
	groupBy?: SmartFolderGroupBy[] | null;
	/**
	 * Include live physical folders, including their content types, typed metadata and taxonomy. Omitted/false selects items only.
	 */
	includeFolders?: boolean | null;
	/**
	 * query saves a read-only live search; classification also permits explicit classify/unclassify actions.
	 */
	mode?: SmartFolderDefinition_mode | null;
	/**
	 * The templates property
	 */
	templates?: string[] | null;
	/**
	 * The term_match property
	 */
	termMatch?: SmartFolderDefinition_term_match | null;
	/**
	 * The terms property
	 */
	terms?: Guid[] | null;
}
export type SmartFolderDefinition_collection_kind = (typeof SmartFolderDefinition_collection_kindObject)[keyof typeof SmartFolderDefinition_collection_kindObject];
export type SmartFolderDefinition_mode = (typeof SmartFolderDefinition_modeObject)[keyof typeof SmartFolderDefinition_modeObject];
export type SmartFolderDefinition_term_match = (typeof SmartFolderDefinition_term_matchObject)[keyof typeof SmartFolderDefinition_term_matchObject];
/**
 * Classify existing item or create one through ordinary item validation/revisions/publication. Current folder_version is required. Inferred AND equalities, selected terms and writable path fields are applied; non-inferable predicates must already fit. The write rolls back unless the head matches the folder/path. Existing items retain location; new parent_id must belong to collection.
 */
export interface SmartFolderDrop extends Parsable {
	/**
	 * The collection_id property
	 */
	collectionId?: Guid | null;
	/**
	 * The create property
	 */
	create?: BulkCreate | null;
	/**
	 * The folder_version property
	 */
	folderVersion?: number | null;
	/**
	 * The item_id property
	 */
	itemId?: Guid | null;
	/**
	 * The parent_id property
	 */
	parentId?: Guid | null;
	/**
	 * One value per navigation level; null means missing. Numeric and boolean values use canonical strings, dates use YYYY or YYYY-MM when grouped.
	 */
	path?: string[] | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface SmartFolderEntry extends AdditionalDataHolder, Parsable {
	/**
	 * The collection_id property
	 */
	collectionId?: Guid | null;
	/**
	 * The collection_kind property
	 */
	collectionKind?: SmartFolderEntry_collection_kind | null;
	/**
	 * The collection_name property
	 */
	collectionName?: string | null;
	/**
	 * The item property
	 */
	item?: Resource | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
export type SmartFolderEntry_collection_kind = (typeof SmartFolderEntry_collection_kindObject)[keyof typeof SmartFolderEntry_collection_kindObject];
export interface SmartFolderGroup extends AdditionalDataHolder, Parsable {
	/**
	 * The count property
	 */
	count?: number | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The value property
	 */
	value?: UntypedNode | null;
}
export interface SmartFolderGroupBy extends Parsable {
	/**
	 * The by property
	 */
	by?: SmartFolderGroupBy_by | null;
	/**
	 * The field property
	 */
	field?: string | null;
}
export type SmartFolderGroupBy_by = (typeof SmartFolderGroupBy_byObject)[keyof typeof SmartFolderGroupBy_byObject];
export interface SmartFolderGroupsResult extends AdditionalDataHolder, Parsable {
	/**
	 * The by property
	 */
	by?: string | null;
	/**
	 * The field property
	 */
	field?: string | null;
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: SmartFolderGroup[] | null;
}
export interface SmartFolderImportResult extends AdditionalDataHolder, Parsable {
	/**
	 * The created property
	 */
	created?: number | null;
	/**
	 * The data property
	 */
	data?: SmartFolder[] | null;
	/**
	 * The updated property
	 */
	updated?: number | null;
	/**
	 * The workspace_version property
	 */
	workspaceVersion?: number | null;
}
/**
 * Personal folders are owner-only and may span all readable workspaces. Shared folders require workspace_id and workspace manage permission. Ownership and workspace are immutable on PUT; PUT replaces the definition. At most 100 shared folders per workspace. Shared mutations advance the workspace version; personal mutations do not.
 */
export interface SmartFolderInput extends Parsable {
	/**
	 * Live query across at most 100 readable collections. Name/key selectors ignore case. Terms include descendants and can occupy different term fields. Incompatible collection filters are skipped; none compatible returns validation. Metadata navigation is scalar and indexed, with compatible field types/scopes across collections. Physical folders have content types and optional custom fields, and participate in taxonomy/content-type selectors when include_folders is true. Equivalent collection predicates share one SQL branch; distinct shapes use UNION ALL. Queries are bounded to 30,000 SQL parameters. Mode is explicit. Results can be read directly without supplying a group path. $file_type filters the MIME type on the authorized visible revision.
	 */
	definition?: SmartFolderDefinition | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The personal property
	 */
	personal?: boolean | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
/**
 * Shared definitions only; personal folders and ownership IDs are excluded. Imports merge by exact unique name, preserve IDs and leave other definitions intact. Target taxonomy must exist. Any invalid entry rolls back the entire package. Identical reapplication changes no versions or audit records.
 */
export interface SmartFolderPackage extends Parsable {
	/**
	 * The folders property
	 */
	folders?: PortableSmartFolder[] | null;
}
export interface SmartFolderPage extends AdditionalDataHolder, Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: SmartFolder[] | null;
}
export interface SmartFolderPreview extends Parsable {
	/**
	 * Live query across at most 100 readable collections. Name/key selectors ignore case. Terms include descendants and can occupy different term fields. Incompatible collection filters are skipped; none compatible returns validation. Metadata navigation is scalar and indexed, with compatible field types/scopes across collections. Physical folders have content types and optional custom fields, and participate in taxonomy/content-type selectors when include_folders is true. Equivalent collection predicates share one SQL branch; distinct shapes use UNION ALL. Queries are bounded to 30,000 SQL parameters. Mode is explicit. Results can be read directly without supplying a group path. $file_type filters the MIME type on the authorized visible revision.
	 */
	definition?: SmartFolderDefinition | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: Guid | null;
}
/**
 * Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink. An empty path returns all matching items even when group_by is configured.
 */
export interface SmartFolderQueryRequest extends Parsable {
	/**
	 * Count every authorized match before pagination. An exact total evaluates the whole result set, so it is returned only when requested.
	 */
	includeTotal?: boolean | null;
	/**
	 * One value per navigation level; null means missing. Numeric and boolean values use canonical strings, dates use YYYY or YYYY-MM when grouped.
	 */
	path?: string[] | null;
	/**
	 * Results default to modified descending. ID ties follow the chosen direction. Group queries use grouping order.
	 */
	sort?: SmartFolderQueryRequest_sort | null;
	/**
	 * The surface property
	 */
	surface?: SmartFolderQueryRequest_surface | null;
}
/**
 * Results default to modified descending. ID ties follow the chosen direction. Group queries use grouping order.
 */
export interface SmartFolderQueryRequest_sort extends Parsable {
	/**
	 * The direction property
	 */
	direction?: SmartFolderQueryRequest_sort_direction | null;
	/**
	 * The field property
	 */
	field?: SmartFolderQueryRequest_sort_field | null;
}
export type SmartFolderQueryRequest_sort_direction = (typeof SmartFolderQueryRequest_sort_directionObject)[keyof typeof SmartFolderQueryRequest_sort_directionObject];
export type SmartFolderQueryRequest_sort_field = (typeof SmartFolderQueryRequest_sort_fieldObject)[keyof typeof SmartFolderQueryRequest_sort_fieldObject];
export type SmartFolderQueryRequest_surface = (typeof SmartFolderQueryRequest_surfaceObject)[keyof typeof SmartFolderQueryRequest_surfaceObject];
/**
 * Authorized items sorted by name, created or modified time, with ID ties in the same direction. Default is modified descending. Cursors bind folder version, caller, surface, path, ordering, schemas and resolved references.
 */
export interface SmartFolderResult extends AdditionalDataHolder, Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The total property
	 */
	total?: number | null;
	/**
	 * Preview counts stop at 10,000. True means total is a lower bound.
	 */
	totalCapped?: boolean | null;
	/**
	 * The value property
	 */
	value?: SmartFolderEntry[] | null;
}
/**
 * Taxonomy set key and root-to-leaf term names, resolved in the organization term store.
 */
export interface SmartFolderTermRef extends Parsable {
	/**
	 * The path property
	 */
	path?: string[] | null;
	/**
	 * The term_set_key property
	 */
	termSetKey?: string | null;
}
export interface SmartFolderUnclassify extends Parsable {
	/**
	 * The folder_version property
	 */
	folderVersion?: number | null;
}
export interface SortSpec extends Parsable {
	/**
	 * The direction property
	 */
	direction?: SortSpec_direction | null;
	/**
	 * The field property
	 */
	field?: string | null;
}
export type SortSpec_direction = (typeof SortSpec_directionObject)[keyof typeof SortSpec_directionObject];
/**
 * The staged file a consumer made of an artifact.
 */
export interface StagedArtifact extends Parsable {
	/**
	 * The artifact_id property
	 */
	artifactId?: string | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The calling workflow run; absent for other callers.
	 */
	runId?: string | null;
}
/**
 * A consumer's claim on an artifact, with the artifact's file: it waits until its consumer promotes it to the item's current file or discards it.
 */
export interface StagedFile extends AdditionalDataHolder, Parsable {
	/**
	 * The artifact the staged file holds.
	 */
	artifactId?: string | null;
	/**
	 * The content_type property
	 */
	contentType?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The filename property
	 */
	filename?: string | null;
	/**
	 * Pending, and every source file is still current.
	 */
	fresh?: boolean | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The metrics property
	 */
	metrics?: StagedFile_metrics | null;
	/**
	 * The workflow run that staged the file; absent when a person or client staged it.
	 */
	runId?: string | null;
	/**
	 * The sha256 property
	 */
	sha256?: string | null;
	/**
	 * The size property
	 */
	size?: number | null;
	/**
	 * The sources property
	 */
	sources?: StagedFile_sources[] | null;
	/**
	 * The state property
	 */
	state?: StagedFile_state | null;
}
export interface StagedFile_metrics extends AdditionalDataHolder, Parsable {
}
export interface StagedFile_sources extends AdditionalDataHolder, Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
}
export type StagedFile_state = (typeof StagedFile_stateObject)[keyof typeof StagedFile_stateObject];
export interface Task extends AdditionalDataHolder, Parsable {
	/**
	 * The caller, or their group, already answered; someone assigned several ways may still answer for the others (can_respond).
	 */
	answered?: boolean | null;
	/**
	 * Subjects and group:<id> entries.
	 */
	assignees?: string[] | null;
	/**
	 * Whether the caller may change the open task's assignees; set when one task is read.
	 */
	canReassign?: boolean | null;
	/**
	 * The caller may answer now.
	 */
	canRespond?: boolean | null;
	/**
	 * The comment property
	 */
	comment?: string | null;
	/**
	 * The completed_at property
	 */
	completedAt?: Date | null;
	/**
	 * The completed_by property
	 */
	completedBy?: string | null;
	/**
	 * The completion property
	 */
	completion?: Task_completion | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The created_by property
	 */
	createdBy?: string | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The due_at property
	 */
	dueAt?: Date | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * Form values of the completing answer; with completion all, the latest answer with the outcome.
	 */
	input?: Task_input | null;
	/**
	 * A JSON Schema form (draft-07 subset; see docs/workflows.md#forms): type, title, description, default, enum, minimum, maximum, minLength, maxLength, minItems, maxItems, uniqueItems, items, properties, required, additionalProperties (boolean or a schema; objects are closed by default). x-papergo on a property makes it a domain picker (item, collection, relationship, terms, keywords, people). The terms picker selects term IDs by default; select: sets selects term-set IDs, optionally narrowed by group_id.
	 */
	inputSchema?: WorkflowForm | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The kind property
	 */
	kind?: Task_kind | null;
	/**
	 * A workflow's task: the operation its answer ends, which the run waits on with ctx.wait. It succeeds with the task's result when the task completes and is cancelled with the task.
	 */
	operationId?: string | null;
	/**
	 * The outcome property
	 */
	outcome?: string | null;
	/**
	 * The outcomes property
	 */
	outcomes?: TaskOutcome[] | null;
	/**
	 * The priority property
	 */
	priority?: Task_priority | null;
	/**
	 * Answers so far, oldest first; in single reads.
	 */
	responses?: TaskAnswer[] | null;
	/**
	 * The review property
	 */
	review?: Task_review | null;
	/**
	 * The run_id property
	 */
	runId?: string | null;
	/**
	 * The source property
	 */
	source?: Task_source | null;
	/**
	 * The status property
	 */
	status?: Task_status | null;
	/**
	 * The title property
	 */
	title?: string | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * Initial form values.
	 */
	values?: Task_values | null;
	/**
	 * The version property
	 */
	version?: number | null;
	/**
	 * The workflow_id property
	 */
	workflowId?: string | null;
	/**
	 * The workspace_id property
	 */
	workspaceId?: string | null;
}
export type Task_completion = (typeof Task_completionObject)[keyof typeof Task_completionObject];
/**
 * Form values of the completing answer; with completion all, the latest answer with the outcome.
 */
export interface Task_input extends AdditionalDataHolder, Parsable {
}
export type Task_kind = (typeof Task_kindObject)[keyof typeof Task_kindObject];
export type Task_priority = (typeof Task_priorityObject)[keyof typeof Task_priorityObject];
export interface Task_review extends AdditionalDataHolder, Parsable {
	/**
	 * The staged_file_id property
	 */
	stagedFileId?: string | null;
	/**
	 * The type property
	 */
	type?: Task_review_type | null;
}
export type Task_review_type = (typeof Task_review_typeObject)[keyof typeof Task_review_typeObject];
export type Task_source = (typeof Task_sourceObject)[keyof typeof Task_sourceObject];
export type Task_status = (typeof Task_statusObject)[keyof typeof Task_statusObject];
/**
 * Initial form values.
 */
export interface Task_values extends AdditionalDataHolder, Parsable {
}
export interface TaskAnswer extends AdditionalDataHolder, Parsable {
	/**
	 * The entry answered for: the subject or group:<id>.
	 */
	assignee?: string | null;
	/**
	 * The comment property
	 */
	comment?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The input property
	 */
	input?: TaskAnswer_input | null;
	/**
	 * The outcome property
	 */
	outcome?: string | null;
	/**
	 * The subject property
	 */
	subject?: string | null;
}
export interface TaskAnswer_input extends AdditionalDataHolder, Parsable {
}
export interface TaskCreate extends Parsable {
	/**
	 * People, group:<id>, role:owners, role:members, creator or field:<key>.
	 */
	assignees?: string[] | null;
	/**
	 * The completion property
	 */
	completion?: TaskCreate_completion | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The due_at property
	 */
	dueAt?: Date | null;
	/**
	 * A JSON Schema form (draft-07 subset; see docs/workflows.md#forms): type, title, description, default, enum, minimum, maximum, minLength, maxLength, minItems, maxItems, uniqueItems, items, properties, required, additionalProperties (boolean or a schema; objects are closed by default). x-papergo on a property makes it a domain picker (item, collection, relationship, terms, keywords, people). The terms picker selects term IDs by default; select: sets selects term-set IDs, optionally narrowed by group_id.
	 */
	inputSchema?: WorkflowForm | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The kind property
	 */
	kind?: TaskCreate_kind | null;
	/**
	 * The outcomes property
	 */
	outcomes?: TaskOutcome[] | null;
	/**
	 * The priority property
	 */
	priority?: TaskCreate_priority | null;
	/**
	 * A staged file of the calling workflow run for the assignees to review: beside the item's current file (file.compare) or alone (file.preview).
	 */
	review?: TaskCreate_review | null;
	/**
	 * The title property
	 */
	title?: string | null;
	/**
	 * The values property
	 */
	values?: TaskCreate_values | null;
}
export type TaskCreate_completion = (typeof TaskCreate_completionObject)[keyof typeof TaskCreate_completionObject];
export type TaskCreate_kind = (typeof TaskCreate_kindObject)[keyof typeof TaskCreate_kindObject];
export type TaskCreate_priority = (typeof TaskCreate_priorityObject)[keyof typeof TaskCreate_priorityObject];
/**
 * A staged file of the calling workflow run for the assignees to review: beside the item's current file (file.compare) or alone (file.preview).
 */
export interface TaskCreate_review extends Parsable {
	/**
	 * The staged_file_id property
	 */
	stagedFileId?: string | null;
	/**
	 * The type property
	 */
	type?: TaskCreate_review_type | null;
}
export type TaskCreate_review_type = (typeof TaskCreate_review_typeObject)[keyof typeof TaskCreate_review_typeObject];
export interface TaskCreate_values extends AdditionalDataHolder, Parsable {
}
export interface TaskList extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: Task[] | null;
}
export interface TaskOutcome extends Parsable {
	/**
	 * The allow_stale property
	 */
	allowStale?: boolean | null;
	/**
	 * The comment property
	 */
	comment?: TaskOutcome_comment | null;
	/**
	 * The decisive property
	 */
	decisive?: boolean | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The skip_form property
	 */
	skipForm?: boolean | null;
	/**
	 * The style property
	 */
	style?: TaskOutcome_style | null;
}
export type TaskOutcome_comment = (typeof TaskOutcome_commentObject)[keyof typeof TaskOutcome_commentObject];
export type TaskOutcome_style = (typeof TaskOutcome_styleObject)[keyof typeof TaskOutcome_styleObject];
export interface TaskReassign extends Parsable {
	/**
	 * People, group:<id>, role:owners, role:members, creator or field:<key>; none means the workspace's owners.
	 */
	assignees?: string[] | null;
}
export interface TaskResponseInput extends Parsable {
	/**
	 * The comment property
	 */
	comment?: string | null;
	/**
	 * Values of the task's form.
	 */
	input?: TaskResponseInput_input | null;
	/**
	 * The outcome property
	 */
	outcome?: string | null;
}
/**
 * Values of the task's form.
 */
export interface TaskResponseInput_input extends AdditionalDataHolder, Parsable {
}
/**
 * Picks active terms of an organization term set for text, data or a document.
 */
export interface TaxonomyDecisionRequest extends Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: string | null;
	/**
	 * With text_id: document context shown with the text.
	 */
	context?: TaxonomyDecisionRequest_context | null;
	/**
	 * JSON data to decide about.
	 */
	data?: TaxonomyDecisionRequest_data | null;
	/**
	 * The description_overrides property
	 */
	descriptionOverrides?: TaxonomyDecisionRequest_description_overrides[] | null;
	/**
	 * Text to decide about.
	 */
	input?: string | null;
	/**
	 * The item_id property
	 */
	itemId?: string | null;
	/**
	 * The leaf_only property
	 */
	leafOnly?: boolean | null;
	/**
	 * The other_text property
	 */
	otherText?: string | null;
	/**
	 * The question property
	 */
	question?: string | null;
	/**
	 * The root_term_id property
	 */
	rootTermId?: string | null;
	/**
	 * multiple (the default) compares each term independently; single makes one joint choice.
	 */
	selection?: TaxonomyDecisionRequest_selection | null;
	/**
	 * The term_ids property
	 */
	termIds?: string[] | null;
	/**
	 * The term_set_id property
	 */
	termSetId?: string | null;
	/**
	 * A text read from the item's current file version blob_id.
	 */
	textId?: string | null;
	/**
	 * Probability a term needs, 0.8 when omitted. Experimental scores, not calibrated confidence.
	 */
	threshold?: number | null;
	/**
	 * Where a decision on input or data is made.
	 */
	workspaceId?: string | null;
}
/**
 * With text_id: document context shown with the text.
 */
export interface TaxonomyDecisionRequest_context extends AdditionalDataHolder, Parsable {
}
/**
 * JSON data to decide about.
 */
export interface TaxonomyDecisionRequest_data extends AdditionalDataHolder, Parsable {
}
export interface TaxonomyDecisionRequest_description_overrides extends Parsable {
	/**
	 * The term_id property
	 */
	termId?: string | null;
	/**
	 * The text property
	 */
	text?: string | null;
}
export type TaxonomyDecisionRequest_selection = (typeof TaxonomyDecisionRequest_selectionObject)[keyof typeof TaxonomyDecisionRequest_selectionObject];
export interface TaxonomyImportResult extends Parsable {
	/**
	 * The groups_created property
	 */
	groupsCreated?: number | null;
	/**
	 * The sets_created property
	 */
	setsCreated?: number | null;
	/**
	 * The terms_created property
	 */
	termsCreated?: number | null;
}
export interface TaxonomyPackage extends Parsable {
	/**
	 * The groups property
	 */
	groups?: PortableTermGroup[] | null;
}
export interface TemplateField extends Parsable {
	/**
	 * The choices property
	 */
	choices?: string[] | null;
	/**
	 * Opt-in typed query projection. Changing it queues a background index build; see index_status.
	 */
	indexed?: boolean | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * The options property
	 */
	options?: FieldOptions | null;
	/**
	 * The required property
	 */
	required?: boolean | null;
	/**
	 * Immutable decimal scale; zero for all other field types. Scaled units must fit signed 64 bits.
	 */
	scale?: number | null;
	/**
	 * The type property
	 */
	type?: TemplateField_type | null;
}
export type TemplateField_type = (typeof TemplateField_typeObject)[keyof typeof TemplateField_typeObject];
export interface TemplateInput extends Parsable {
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The fields property
	 */
	fields?: TemplateField[] | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The rules property
	 */
	rules?: (ValidationRuleMember1 | ValidationRuleMember2)[] | null;
}
export interface Term extends Parsable {
	/**
	 * The available_as_keyword property
	 */
	availableAsKeyword?: boolean | null;
	/**
	 * The color property
	 */
	color?: string | null;
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The deprecated property
	 */
	deprecated?: boolean | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The has_children property
	 */
	hasChildren?: boolean | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The labels property
	 */
	labels?: Term_labels | null;
	/**
	 * Set on a merged term: the term that replaces it.
	 */
	mergedIntoId?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The normalized_name property
	 */
	normalizedName?: string | null;
	/**
	 * The parent_id property
	 */
	parentId?: string | null;
	/**
	 * Ids from the root to the term: /root/…/term/.
	 */
	path?: string | null;
	/**
	 * The sort_order property
	 */
	sortOrder?: number | null;
	/**
	 * The synonyms property
	 */
	synonyms?: string[] | null;
	/**
	 * The term_set_id property
	 */
	termSetId?: string | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface Term_labels extends AdditionalDataHolder, Parsable {
}
export interface TermEntries extends Parsable {
	/**
	 * The items property
	 */
	items?: string[] | null;
	/**
	 * The after of the next page; omitted on the last.
	 */
	next?: string | null;
}
export interface TermGroup extends Parsable {
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The organization's system group, which holds the keywords set; it cannot change.
	 */
	isSystem?: boolean | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface TermGroupInput extends Parsable {
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
export interface TermGroupPage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: TermGroup[] | null;
}
export interface TermImportResult extends Parsable {
	/**
	 * The sets_created property
	 */
	setsCreated?: number | null;
	/**
	 * The terms_created property
	 */
	termsCreated?: number | null;
	/**
	 * The term_sets property
	 */
	termSets?: TermSet[] | null;
}
export interface TermInput extends Parsable {
	/**
	 * Offer this term as a keyword; not for terms of the keywords set.
	 */
	availableAsKeyword?: boolean | null;
	/**
	 * The color property
	 */
	color?: string | null;
	/**
	 * The deprecated property
	 */
	deprecated?: boolean | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The labels property
	 */
	labels?: TermInput_labels | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * Parent of a new term; use the move operation to change it.
	 */
	parentId?: string | null;
	/**
	 * The sort_order property
	 */
	sortOrder?: number | null;
	/**
	 * The synonyms property
	 */
	synonyms?: string[] | null;
}
export interface TermInput_labels extends AdditionalDataHolder, Parsable {
}
export interface TermList extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: Term[] | null;
}
export interface TermPage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: Term[] | null;
}
export interface TermSet extends Parsable {
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * The group_id property
	 */
	groupId?: string | null;
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The organization's keywords set.
	 */
	isKeywords?: boolean | null;
	/**
	 * The is_open property
	 */
	isOpen?: boolean | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The updated_at property
	 */
	updatedAt?: Date | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface TermSetInput extends Parsable {
	/**
	 * The description property
	 */
	description?: string | null;
	/**
	 * Required on create; immutable.
	 */
	groupId?: string | null;
	/**
	 * Open sets accept new terms from people with read access. Curation requires manage access.
	 */
	isOpen?: boolean | null;
	/**
	 * Required on create; immutable.
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
export interface TermSetPage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: TermSet[] | null;
}
/**
 * Uses container If-Match. Key, type and scale are immutable. Changes affect future revisions; historical content retains its schema.
 */
export interface UpdateField extends Parsable {
	/**
	 * The choices property
	 */
	choices?: string[] | null;
	/**
	 * The indexed property
	 */
	indexed?: boolean | null;
	/**
	 * The label property
	 */
	label?: string | null;
	/**
	 * Complete replacement of options; multiple and reference scope are immutable.
	 */
	options?: FieldOptions | null;
	/**
	 * The required property
	 */
	required?: boolean | null;
}
export interface UpdateListIntegration extends Parsable {
	/**
	 * Provider-owned private configuration. For ical_https, supply an HTTPS iCalendar URL.
	 */
	configuration?: UpdateListIntegration_configuration | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * The paused property
	 */
	paused?: boolean | null;
	/**
	 * Requires configuration. Detach existing replicas and start new external identities for a different feed.
	 */
	reset?: boolean | null;
}
/**
 * Provider-owned private configuration. For ical_https, supply an HTTPS iCalendar URL.
 */
export interface UpdateListIntegration_configuration extends Parsable {
	/**
	 * The url property
	 */
	url?: string | null;
}
export interface UpdateRelationship extends Parsable {
	/**
	 * Per-key patch: omitted keys retain their values; null removes a key. The resulting metadata must satisfy the relationship type.
	 */
	metadata?: UpdateRelationship_metadata | null;
}
/**
 * Per-key patch: omitted keys retain their values; null removes a key. The resulting metadata must satisfy the relationship type.
 */
export interface UpdateRelationship_metadata extends AdditionalDataHolder, Parsable {
}
export interface UpdateResource extends Parsable {
	/**
	 * Lists and libraries only: permits creating physical folders. Disabling retains existing folders. Changes require manage access.
	 */
	allowFolders?: boolean | null;
	/**
	 * Items and folders: an attached collection content type. Creation selects the default when omitted. PATCH changes type, keeps shared fields and removes fields absent from the destination type. Folder required fields and creation defaults do not apply.
	 */
	contentTypeId?: Guid | null;
	/**
	 * Workspace only. Empty inherits the organization default. Changing this affects newly created currency fields, not existing definitions.
	 */
	defaultCurrency?: string | null;
	/**
	 * Workspace or collection description, limited to 2000 UTF-16 code units. Changes require manage access.
	 */
	description?: string | null;
	/**
	 * Libraries only. Default warn. Compares hashes against current documents visible to the caller across libraries and workspaces. Allow suppresses warnings; block rejects duplicates with 409 duplicate_file.
	 */
	duplicatePolicy?: UpdateResource_duplicate_policy | null;
	/**
	 * Shared folksonomy keywords. Responses contain term IDs. Writes accept existing IDs or trimmed labels; new labels create terms in the organization Keywords term set. Keywords fields use the same vocabulary.
	 */
	keywords?: string[] | null;
	/**
	 * History entries kept per item, trimmed on each new recorded version. Defaults to 50.
	 */
	maxVersions?: number | null;
	/**
	 * Item and folder titles contain at most 1024 UTF-16 units before trimming. Workspace and collection names allow 200 units. Library names also exclude path separators and control characters and are unique among live siblings ignoring case.
	 */
	name?: string | null;
	/**
	 * Moves a folder or item below another folder of the same list or library, or to its top level. Requires write access to the new parent. A move alone creates no content revision; inheriting resources take the new parent's permission scope.
	 */
	parentId?: Guid | null;
	/**
	 * Lists/libraries only. False publishes content changes immediately. Changing policy requires manage access; disabling publishes all current heads atomically.
	 */
	publishingEnabled?: boolean | null;
	/**
	 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
	 */
	values?: UpdateResource_values | null;
	/**
	 * Collections only. Lists and libraries default to off. Off stops recording new history; retained entries remain available. Replacing a file while off queues its previous bytes for reclamation after the one-hour grace period, subject to current/published file, staged-use and workflow-lease protection.
	 */
	versioning?: UpdateResource_versioning | null;
	/**
	 * Libraries only; changing it requires manage access.
	 */
	webdavEnabled?: boolean | null;
}
export type UpdateResource_duplicate_policy = (typeof UpdateResource_duplicate_policyObject)[keyof typeof UpdateResource_duplicate_policyObject];
/**
 * Custom fields on items and folders, validated against their attached collection content type. Folder fields are optional and receive no creation defaults. Folder edits are immediately visible; items follow publishing settings.
 */
export interface UpdateResource_values extends AdditionalDataHolder, Parsable {
}
export type UpdateResource_versioning = (typeof UpdateResource_versioningObject)[keyof typeof UpdateResource_versioningObject];
export type ValidationRule = ValidationRuleMember1 | ValidationRuleMember2;
export interface ValidationRuleMember1 extends AdditionalDataHolder, Parsable {
	/**
	 * The op property
	 */
	op?: UntypedNode | null;
}
export interface ValidationRuleMember2 extends AdditionalDataHolder, Parsable {
	/**
	 * The op property
	 */
	op?: ValidationRuleMember2_op | null;
}
export type ValidationRuleMember2_op = (typeof ValidationRuleMember2_opObject)[keyof typeof ValidationRuleMember2_opObject];
/**
 * Collection-managed saved view. Columns use field keys and $id/$name/$keywords; empty columns default to $name. PUT replaces the definition. Layout is a client hint.
 */
export interface ViewInput extends Parsable {
	/**
	 * The columns property
	 */
	columns?: string[] | null;
	/**
	 * The is_default property
	 */
	isDefault?: boolean | null;
	/**
	 * The layout property
	 */
	layout?: ViewInput_layout | null;
	/**
	 * The name property
	 */
	name?: string | null;
	/**
	 * Collection query over visible surfaces. System fields: $id, $name, $keywords, $created_at, $created_by, $modified_at, $modified_by. Times are RFC3339 UTC and modification metadata follows the selected revision. Archive filters apply before paging, counts, and grouping. $archived_at and $archived_by are system fields usable in columns, filters, and sorting.
	 */
	query?: QuerySpec | null;
}
export type ViewInput_layout = (typeof ViewInput_layoutObject)[keyof typeof ViewInput_layoutObject];
/**
 * Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
 */
export interface ViewQueryRequest extends Parsable {
	/**
	 * Count every authorized match before pagination. An exact total evaluates the whole result set, so it is returned only when requested.
	 */
	includeTotal?: boolean | null;
	/**
	 * Selected custom fields and optional $keywords; system columns are accepted and the resource metadata envelope remains present. Omitted returns full values on direct queries and saved columns on views; an empty array returns no custom values. Selected fields need not be indexed. Cursors bind this selection.
	 */
	select?: string[] | null;
	/**
	 * The surface property
	 */
	surface?: ViewQueryRequest_surface | null;
}
export type ViewQueryRequest_surface = (typeof ViewQueryRequest_surfaceObject)[keyof typeof ViewQueryRequest_surfaceObject];
/**
 * A JSON Schema form (draft-07 subset; see docs/workflows.md#forms): type, title, description, default, enum, minimum, maximum, minLength, maxLength, minItems, maxItems, uniqueItems, items, properties, required, additionalProperties (boolean or a schema; objects are closed by default). x-papergo on a property makes it a domain picker (item, collection, relationship, terms, keywords, people). The terms picker selects term IDs by default; select: sets selects term-set IDs, optionally narrowed by group_id.
 */
export interface WorkflowForm extends AdditionalDataHolder, Parsable {
}
export interface WorkspaceKeywordCount extends Parsable {
	/**
	 * Resources the caller can read that carry the keyword.
	 */
	count?: number | null;
	/**
	 * Keyword term ID in the shared vocabulary.
	 */
	keyword?: string | null;
	/**
	 * The current keyword name.
	 */
	name?: string | null;
}
export interface WorkspaceKeywordCountPage extends Parsable {
	/**
	 * Absolute continuation URL, or null on the final page. Repeat the request method and body.
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: WorkspaceKeywordCount[] | null;
}
export interface WorkspaceMember extends Parsable {
	/**
	 * Owner manages the workspace and all descendants; member contributes; visitor reads published content.
	 */
	role?: WorkspaceMember_role | null;
	/**
	 * Active organization directory subject. Inactive users cannot be added or promoted. Verified bearer or trusted-proxy subjects enroll on first authentication; user managers can provision subjects through POST /v1/identity/users.
	 */
	subject?: string | null;
}
export type WorkspaceMember_role = (typeof WorkspaceMember_roleObject)[keyof typeof WorkspaceMember_roleObject];
export interface WorkspaceRelationshipPage extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: WorkspaceRelationshipView[] | null;
}
export interface WorkspaceRelationshipType extends AdditionalDataHolder, Parsable {
	/**
	 * The directed property
	 */
	directed?: boolean | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The inverse_label property
	 */
	inverseLabel?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
export interface WorkspaceRelationshipTypePage extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: WorkspaceRelationshipType[] | null;
}
/**
 * A compact workspace relationship. Untyped links omit type_id and use the label Untyped. Full edge metadata and version are available through the per-item relationship read.
 */
export interface WorkspaceRelationshipView extends AdditionalDataHolder, Parsable {
	/**
	 * The created_at property
	 */
	createdAt?: Date | null;
	/**
	 * The directed property
	 */
	directed?: boolean | null;
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The inverse_label property
	 */
	inverseLabel?: string | null;
	/**
	 * The source_id property
	 */
	sourceId?: Guid | null;
	/**
	 * Compact navigation information from the endpoint surface visible to the reader.
	 */
	sourceItem?: RelationshipEndpoint | null;
	/**
	 * The target_id property
	 */
	targetId?: Guid | null;
	/**
	 * Compact navigation information from the endpoint surface visible to the reader.
	 */
	targetItem?: RelationshipEndpoint | null;
	/**
	 * The type_id property
	 */
	typeId?: Guid | null;
	/**
	 * The type_name property
	 */
	typeName?: string | null;
}
declare const ArchiveEntry_duplicate_policyObject: {
	readonly Allow: "allow";
	readonly Warn: "warn";
	readonly Block: "block";
};
declare const ArchiveEntry_kindObject: {
	readonly Workspace: "workspace";
	readonly List: "list";
	readonly Library: "library";
	readonly Folder: "folder";
	readonly Item: "item";
};
declare const ArchiveEntry_system_keyObject: {
	readonly HomeDocuments: "home.documents";
	readonly HomeInbox: "home.inbox";
	readonly OpenapiJsonNullSentinelValue2BF936000FE44250987AE5DDB203E464: "openapi-json-null-sentinel-value-2BF93600-0FE4-4250-987A-E5DDB203E464";
};
declare const ArchiveEntry_versioningObject: {
	readonly Off: "off";
	readonly Major: "major";
};
declare const ArchiveSelectionObject: {
	readonly Active: "active";
	readonly Archived: "archived";
	readonly All: "all";
};
declare const Blob_content_typeObject: {
	readonly ApplicationPdf: "application/pdf";
	readonly ImageTiff: "image/tiff";
	readonly ImageJpeg: "image/jpeg";
	readonly ImagePng: "image/png";
	readonly ImageWebp: "image/webp";
};
declare const BulkResult_actionObject: {
	readonly Create: "create";
	readonly Update: "update";
	readonly Publish: "publish";
	readonly Unpublish: "unpublish";
	readonly Delete: "delete";
	readonly Archive: "archive";
	readonly Unarchive: "unarchive";
};
declare const CalendarQuery_surfaceObject: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
declare const ChunkText_chunkerObject: {
	readonly Window: "window";
	readonly Pages: "pages";
	readonly Paragraphs: "paragraphs";
	readonly Headings: "headings";
	readonly Whole: "whole";
};
declare const ChunkText_kindObject: {
	readonly Content: "content";
	readonly Summary: "summary";
	readonly Extract: "extract";
};
declare const CollectionTemplate_duplicate_policyObject: {
	readonly Allow: "allow";
	readonly Warn: "warn";
	readonly Block: "block";
};
declare const CollectionTemplate_kindObject: {
	readonly List: "list";
	readonly Library: "library";
};
declare const CollectionTemplate_versioningObject: {
	readonly Off: "off";
	readonly Major: "major";
};
declare const CompletionRequest_executionObject: {
	readonly Immediate: "immediate";
	readonly Batch: "batch";
};
declare const CreateCatalogField_typeObject: {
	readonly Text: "text";
	readonly Note: "note";
	readonly Email: "email";
	readonly Url: "url";
	readonly Date: "date";
	readonly Datetime: "datetime";
	readonly Choice: "choice";
	readonly Integer: "integer";
	readonly Decimal: "decimal";
	readonly Number: "number";
	readonly Currency: "currency";
	readonly Boolean: "boolean";
	readonly Lookup: "lookup";
	readonly Term: "term";
	readonly Keywords: "keywords";
	readonly Person: "person";
};
declare const CreateField_typeObject: {
	readonly Text: "text";
	readonly Note: "note";
	readonly Email: "email";
	readonly Url: "url";
	readonly Date: "date";
	readonly Datetime: "datetime";
	readonly Choice: "choice";
	readonly Integer: "integer";
	readonly Decimal: "decimal";
	readonly Number: "number";
	readonly Currency: "currency";
	readonly Boolean: "boolean";
	readonly Lookup: "lookup";
	readonly Term: "term";
	readonly Keywords: "keywords";
	readonly Person: "person";
};
declare const CreateResource_duplicate_policyObject: {
	readonly Allow: "allow";
	readonly Warn: "warn";
	readonly Block: "block";
};
declare const CreateResource_kindObject: {
	readonly List: "list";
	readonly Library: "library";
	readonly Folder: "folder";
	readonly Item: "item";
};
declare const CreateResource_versioningObject: {
	readonly Off: "off";
	readonly Major: "major";
};
declare const DecisionRequest_questions_typeObject: {
	readonly Choice: "choice";
	readonly Noul: "noul";
	readonly Score: "score";
};
declare const DeltaPage_valueMember1_Removed_reasonObject: {
	readonly Deleted: "deleted";
	readonly Changed: "changed";
};
declare const FieldDefinition_index_statusObject: {
	readonly Ready: "ready";
	readonly Building: "building";
	readonly Failed: "failed";
};
declare const FieldDefinition_typeObject: {
	readonly Text: "text";
	readonly Note: "note";
	readonly Email: "email";
	readonly Url: "url";
	readonly Date: "date";
	readonly Datetime: "datetime";
	readonly Choice: "choice";
	readonly Integer: "integer";
	readonly Decimal: "decimal";
	readonly Number: "number";
	readonly Currency: "currency";
	readonly Boolean: "boolean";
	readonly Lookup: "lookup";
	readonly Term: "term";
	readonly Keywords: "keywords";
	readonly Person: "person";
};
declare const FieldOptions_search_weightObject: {
	readonly None: "none";
	readonly Normal: "normal";
	readonly High: "high";
};
declare const FileVersion_availabilityObject: {
	readonly Available: "available";
	readonly Pending: "pending";
	readonly Reclaimed: "reclaimed";
};
declare const FilterExpr_opObject: {
	readonly Eq: "eq";
	readonly Ne: "ne";
	readonly Gt: "gt";
	readonly Gte: "gte";
	readonly Lt: "lt";
	readonly Lte: "lte";
	readonly InEscaped: "in";
	readonly Contains: "contains";
	readonly Under: "under";
	readonly Missing: "missing";
	readonly Present: "present";
};
declare const FilterExpr_value_refObject: {
	readonly Me: "me";
	readonly Today: "today";
	readonly Yesterday: "yesterday";
	readonly Tomorrow: "tomorrow";
	readonly WeekStart: "weekStart";
	readonly WeekEnd: "weekEnd";
	readonly MonthStart: "monthStart";
	readonly MonthEnd: "monthEnd";
	readonly Last7Days: "last7Days";
	readonly Next7Days: "next7Days";
	readonly Last30Days: "last30Days";
	readonly Next30Days: "next30Days";
};
declare const ListIntegration_kindObject: {
	readonly Ical_https: "ical_https";
};
declare const ListView_system_keyObject: {
	readonly Archived: "archived";
};
declare const Operation_error_codeObject: {
	readonly Deadline_exceeded: "deadline_exceeded";
	readonly Retries_exhausted: "retries_exhausted";
	readonly Forbidden: "forbidden";
};
declare const Operation_statusObject: {
	readonly Pending: "pending";
	readonly Succeeded: "succeeded";
	readonly Failed: "failed";
	readonly Superseded: "superseded";
	readonly Cancelled: "cancelled";
};
declare const Operation_waitingObject: {
	readonly Queued: "queued";
	readonly Running: "running";
	readonly Retry: "retry";
	readonly Scheduled: "scheduled";
	readonly Remote: "remote";
};
declare const Permissions_effective_grants_actionObject: {
	readonly Read: "read";
	readonly Read_draft: "read_draft";
	readonly Write: "write";
	readonly Publish: "publish";
	readonly Manage: "manage";
};
declare const Permissions_effective_grants_effectObject: {
	readonly Allow: "allow";
};
declare const Permissions_effective_grants_principal_typeObject: {
	readonly User: "user";
	readonly Group: "group";
	readonly Workspace_visitor: "workspace_visitor";
	readonly Workspace_member: "workspace_member";
	readonly Workspace_owner: "workspace_owner";
};
declare const Permissions_grants_actionObject: {
	readonly Read: "read";
	readonly Read_draft: "read_draft";
	readonly Write: "write";
	readonly Publish: "publish";
	readonly Manage: "manage";
};
declare const Permissions_grants_effectObject: {
	readonly Allow: "allow";
};
declare const Permissions_grants_principal_typeObject: {
	readonly User: "user";
	readonly Group: "group";
	readonly Workspace_visitor: "workspace_visitor";
	readonly Workspace_member: "workspace_member";
	readonly Workspace_owner: "workspace_owner";
};
declare const PortableSmartFolderDefinition_collection_kindObject: {
	readonly List: "list";
	readonly Library: "library";
};
declare const PortableSmartFolderDefinition_modeObject: {
	readonly Query: "query";
	readonly Classification: "classification";
};
declare const PortableSmartFolderDefinition_term_matchObject: {
	readonly All: "all";
	readonly Any: "any";
};
declare const Publication_actionObject: {
	readonly Publish: "publish";
	readonly Unpublish: "unpublish";
};
declare const QueryRequest_surfaceObject: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
declare const ReadText_ocrObject: {
	readonly Auto: "auto";
	readonly Always: "always";
	readonly Never: "never";
};
declare const Resource_duplicate_policyObject: {
	readonly Allow: "allow";
	readonly Warn: "warn";
	readonly Block: "block";
};
declare const Resource_kindObject: {
	readonly Workspace: "workspace";
	readonly List: "list";
	readonly Library: "library";
	readonly Folder: "folder";
	readonly Item: "item";
};
declare const Resource_system_keyObject: {
	readonly HomeDocuments: "home.documents";
	readonly HomeInbox: "home.inbox";
	readonly OpenapiJsonNullSentinelValue2BF936000FE44250987AE5DDB203E464: "openapi-json-null-sentinel-value-2BF93600-0FE4-4250-987A-E5DDB203E464";
};
declare const Resource_versioningObject: {
	readonly Off: "off";
	readonly Major: "major";
};
declare const ResourceAccess_accessObject: {
	readonly Read: "read";
	readonly Read_draft: "read_draft";
	readonly Write: "write";
	readonly Publish: "publish";
	readonly Manage: "manage";
};
declare const SchemaRevision_definition_fields_typeObject: {
	readonly Text: "text";
	readonly Number: "number";
	readonly Currency: "currency";
	readonly Integer: "integer";
	readonly Decimal: "decimal";
	readonly Boolean: "boolean";
	readonly Datetime: "datetime";
	readonly Choice: "choice";
};
declare const SearchField_operatorsObject: {
	readonly Eq: "eq";
	readonly Ne: "ne";
	readonly Gt: "gt";
	readonly Gte: "gte";
	readonly Lt: "lt";
	readonly Lte: "lte";
	readonly InEscaped: "in";
	readonly Under: "under";
	readonly Present: "present";
	readonly Missing: "missing";
};
declare const SearchHit_kindObject: {
	readonly Item: "item";
	readonly Folder: "folder";
};
declare const SearchHit_matched_byObject: {
	readonly Keyword: "keyword";
	readonly Semantic: "semantic";
};
declare const SearchHit_surfaceObject: {
	readonly Head: "head";
	readonly Published: "published";
};
declare const SearchPage_modeObject: {
	readonly Keyword: "keyword";
	readonly Semantic: "semantic";
	readonly Hybrid: "hybrid";
};
declare const SearchRequest_kindObject: {
	readonly Item: "item";
	readonly Folder: "folder";
};
declare const SearchRequest_modeObject: {
	readonly Keyword: "keyword";
	readonly Semantic: "semantic";
	readonly Hybrid: "hybrid";
};
declare const SearchRequest_surfaceObject: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
declare const SetWorkspaceMember_roleObject: {
	readonly Owner: "owner";
	readonly Member: "member";
	readonly Visitor: "visitor";
};
declare const SmartFolderDefinition_collection_kindObject: {
	readonly List: "list";
	readonly Library: "library";
};
declare const SmartFolderDefinition_modeObject: {
	readonly Query: "query";
	readonly Classification: "classification";
};
declare const SmartFolderDefinition_term_matchObject: {
	readonly All: "all";
	readonly Any: "any";
};
declare const SmartFolderEntry_collection_kindObject: {
	readonly List: "list";
	readonly Library: "library";
};
declare const SmartFolderGroupBy_byObject: {
	readonly Year: "year";
	readonly Month: "month";
};
declare const SmartFolderQueryRequest_sort_directionObject: {
	readonly Asc: "asc";
	readonly Desc: "desc";
};
declare const SmartFolderQueryRequest_sort_fieldObject: {
	readonly Name: "$name";
	readonly Created_at: "$created_at";
	readonly Modified_at: "$modified_at";
};
declare const SmartFolderQueryRequest_surfaceObject: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
declare const SortSpec_directionObject: {
	readonly Asc: "asc";
	readonly Desc: "desc";
};
declare const StagedFile_stateObject: {
	readonly Pending: "pending";
	readonly Promoted: "promoted";
	readonly Discarded: "discarded";
};
declare const Task_completionObject: {
	readonly Any: "any";
	readonly All: "all";
};
declare const Task_kindObject: {
	readonly Task: "task";
	readonly Approval: "approval";
};
declare const Task_priorityObject: {
	readonly Low: "low";
	readonly Normal: "normal";
	readonly High: "high";
};
declare const Task_review_typeObject: {
	readonly FileCompare: "file.compare";
	readonly FilePreview: "file.preview";
};
declare const Task_sourceObject: {
	readonly Workflow: "workflow";
	readonly User: "user";
	readonly System: "system";
};
declare const Task_statusObject: {
	readonly Open: "open";
	readonly Completed: "completed";
	readonly Cancelled: "cancelled";
	readonly Expired: "expired";
};
declare const TaskCreate_completionObject: {
	readonly Any: "any";
	readonly All: "all";
};
declare const TaskCreate_kindObject: {
	readonly Task: "task";
	readonly Approval: "approval";
};
declare const TaskCreate_priorityObject: {
	readonly Low: "low";
	readonly Normal: "normal";
	readonly High: "high";
};
declare const TaskCreate_review_typeObject: {
	readonly FileCompare: "file.compare";
	readonly FilePreview: "file.preview";
};
declare const TaskOutcome_commentObject: {
	readonly None: "none";
	readonly Optional: "optional";
	readonly Required: "required";
};
declare const TaskOutcome_styleObject: {
	readonly Primary: "primary";
	readonly Danger: "danger";
	readonly DefaultEscaped: "default";
};
declare const TaxonomyDecisionRequest_selectionObject: {
	readonly Multiple: "multiple";
	readonly Single: "single";
};
declare const TemplateField_typeObject: {
	readonly Text: "text";
	readonly Note: "note";
	readonly Email: "email";
	readonly Url: "url";
	readonly Date: "date";
	readonly Datetime: "datetime";
	readonly Choice: "choice";
	readonly Integer: "integer";
	readonly Decimal: "decimal";
	readonly Number: "number";
	readonly Currency: "currency";
	readonly Boolean: "boolean";
	readonly Lookup: "lookup";
	readonly Term: "term";
	readonly Keywords: "keywords";
	readonly Person: "person";
};
declare const UpdateResource_duplicate_policyObject: {
	readonly Allow: "allow";
	readonly Warn: "warn";
	readonly Block: "block";
};
declare const UpdateResource_versioningObject: {
	readonly Off: "off";
	readonly Major: "major";
};
declare const ValidationRuleMember2_opObject: {
	readonly Eq: "eq";
	readonly Ne: "ne";
	readonly Gt: "gt";
	readonly Gte: "gte";
	readonly Lt: "lt";
	readonly Lte: "lte";
};
declare const ViewInput_layoutObject: {
	readonly Table: "table";
	readonly Board: "board";
	readonly Calendar: "calendar";
	readonly Gallery: "gallery";
};
declare const ViewQueryRequest_surfaceObject: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
declare const WorkspaceMember_roleObject: {
	readonly Owner: "owner";
	readonly Member: "member";
	readonly Visitor: "visitor";
};
/**
 * Builds and executes requests for operations under /v1/ai/completions
 */
export interface CompletionsRequestBuilder extends BaseRequestBuilder<CompletionsRequestBuilder> {
	/**
	 * Asks the configured chat model to do something with text or an image: summarize it, extract values, clean it up. Immediate completions call the model as soon as a worker is free; batch completions wait for their queue's group and are sent as a provider batch. Answers are cached: the same model, prompt, schema and input reuse the answer. Requires document.read and access to the item or workspace. 422 without the model the execution needs.
	 * @param body A completion. Give item_id with text_id or image_blob_id, else workspace_id.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(body: CompletionRequest, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Asks the configured chat model to do something with text or an image: summarize it, extract values, clean it up. Immediate completions call the model as soon as a worker is free; batch completions wait for their queue's group and are sent as a provider batch. Answers are cached: the same model, prompt, schema and input reuse the answer. Requires document.read and access to the item or workspace. 422 without the model the execution needs.
	 * @param body A completion. Give item_id with text_id or image_blob_id, else workspace_id.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: CompletionRequest, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/ai/decisions
 */
export interface DecisionsRequestBuilder extends BaseRequestBuilder<DecisionsRequestBuilder> {
	/**
	 * Answers ordered choice, noul or ordinal score questions about text, JSON data or a document with probability scores. Requires document.read. 422 without the decision engine.
	 * @param body Questions about text, JSON data or a document: exactly one of input, data or text_id.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(body: DecisionRequest, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Answers ordered choice, noul or ordinal score questions about text, JSON data or a document with probability scores. Requires document.read. 422 without the decision engine.
	 * @param body Questions about text, JSON data or a document: exactly one of input, data or text_id.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: DecisionRequest, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/ai/taxonomy-decisions
 */
export interface TaxonomyDecisionsRequestBuilder extends BaseRequestBuilder<TaxonomyDecisionsRequestBuilder> {
	/**
	 * Evaluates active terms of an organization term set and returns the accepted term IDs, every candidate's score and a taxonomy guard for updating the item. The operation fails when the item's file or the term set changed while it ran. Requires document.read and taxonomy read access. 422 without the decision engine.
	 * @param body Picks active terms of an organization term set for text, data or a document.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(body: TaxonomyDecisionRequest, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Evaluates active terms of an organization term set and returns the accepted term IDs, every candidate's score and a taxonomy guard for updating the item. The operation fails when the item's file or the term set changed while it ran. Requires document.read and taxonomy read access. 422 without the decision engine.
	 * @param body Picks active terms of an organization term set for text, data or a document.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TaxonomyDecisionRequest, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/ai
 */
export interface AiRequestBuilder extends BaseRequestBuilder<AiRequestBuilder> {
	/**
	 * The completions property
	 */
	get completions(): CompletionsRequestBuilder;
	/**
	 * The decisions property
	 */
	get decisions(): DecisionsRequestBuilder;
	/**
	 * The taxonomyDecisions property
	 */
	get taxonomyDecisions(): TaxonomyDecisionsRequestBuilder;
}
export interface ArchiveGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: ArchiveEntry[] | null;
}
/**
 * Builds and executes requests for operations under /v1/archive
 */
export interface ArchiveRequestBuilder extends BaseRequestBuilder<ArchiveRequestBuilder> {
	/**
	 * Database-backed name search and filters; newest archived first with keyset pagination. Works without a search service. Requires list.read. Archive date bounds are inclusive.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ArchiveGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ArchiveRequestBuilderGetQueryParameters> | undefined): Promise<ArchiveGetResponse | undefined>;
	/**
	 * Database-backed name search and filters; newest archived first with keyset pagination. Works without a search service. Requires list.read. Archive date bounds are inclusive.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ArchiveRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Database-backed name search and filters; newest archived first with keyset pagination. Works without a search service. Requires list.read. Archive date bounds are inclusive.
 */
export interface ArchiveRequestBuilderGetQueryParameters {
	archivedBy?: string;
	archivedFrom?: Date;
	archivedTo?: Date;
	collectionId?: string;
	contentTypeId?: string;
	keyword?: string;
	q?: string;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
	workspaceId?: string;
}
/**
 * Builds and executes requests for operations under /v1/artifacts/{id}/content
 */
export interface ContentRequestBuilder extends BaseRequestBuilder<ContentRequestBuilder> {
	/**
	 * The artifact's bytes, inline, to readers of its item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ArrayBuffer>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ArrayBuffer | undefined>;
	/**
	 * The artifact's bytes, inline, to readers of its item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/artifacts/{id}/stage
 */
export interface StageRequestBuilder extends BaseRequestBuilder<StageRequestBuilder> {
	/**
	 * Claims an artifact as a staged file for the caller: for the calling workflow run, or for a person or client without one, who alone can then promote or discard it. Requires write access to the item, and fails with 409 when a file it was made from is no longer current.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<StagedArtifact>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<StagedArtifact | undefined>;
	/**
	 * Claims an artifact as a staged file for the caller: for the calling workflow run, or for a person or client without one, who alone can then promote or discard it. Requires write access to the item, and fails with 409 when a file it was made from is no longer current.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/artifacts/{id}
 */
export interface ArtifactsItemRequestBuilder extends BaseRequestBuilder<ArtifactsItemRequestBuilder> {
	/**
	 * The content property
	 */
	get content(): ContentRequestBuilder;
	/**
	 * The stage property
	 */
	get stage(): StageRequestBuilder;
	/**
	 * An artifact an operation made, such as a smaller image or a composed PDF, to readers of its item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Artifact>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Artifact | undefined>;
	/**
	 * An artifact an operation made, such as a smaller image or a composed PDF, to readers of its item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/artifacts
 */
export interface ArtifactsRequestBuilder extends BaseRequestBuilder<ArtifactsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.artifacts.item collection
	 * @param id Unique identifier of the item
	 * @returns {ArtifactsItemRequestBuilder}
	 */
	byId(id: string): ArtifactsItemRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/audit
 */
export interface AuditRequestBuilder extends BaseRequestBuilder<AuditRequestBuilder> {
	/**
	 * Requires audit.read. Newest first by creation time then ID, with opaque keyset paging. Optional actor, entity type/ID, workspace and RFC3339 date filters. from is inclusive; to is exclusive. Properties contain changed names, without values. Trace IDs correlate requests or background writes. Identity, catalog, taxonomy, workflow and content changes share this feed.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<AuditPage>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<AuditRequestBuilderGetQueryParameters> | undefined): Promise<AuditPage | undefined>;
	/**
	 * Requires audit.read. Newest first by creation time then ID, with opaque keyset paging. Optional actor, entity type/ID, workspace and RFC3339 date filters. from is inclusive; to is exclusive. Properties contain changed names, without values. Trace IDs correlate requests or background writes. Identity, catalog, taxonomy, workflow and content changes share this feed.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<AuditRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires audit.read. Newest first by creation time then ID, with opaque keyset paging. Optional actor, entity type/ID, workspace and RFC3339 date filters. from is inclusive; to is exclusive. Properties contain changed names, without values. Trace IDs correlate requests or background writes. Identity, catalog, taxonomy, workflow and content changes share this feed.
 */
export interface AuditRequestBuilderGetQueryParameters {
	actor?: string;
	entityId?: string;
	entityType?: string;
	from?: Date;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	to?: Date;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
	workspaceId?: string;
}
/**
 * Builds and executes requests for operations under /v1/collection-templates
 */
export interface CollectionTemplatesRequestBuilder extends BaseRequestBuilder<CollectionTemplatesRequestBuilder> {
	/**
	 * Requires list.read. Built-in Documents and Contacts and templates registered by modules. Create a collection with template_key to provision it.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<CollectionTemplate[]>}
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<CollectionTemplate[] | undefined>;
	/**
	 * Requires list.read. Built-in Documents and Contacts and templates registered by modules. Create a collection with template_key to provision it.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/content-types/{id}
 */
export interface ContentTypesItemRequestBuilder extends BaseRequestBuilder<ContentTypesItemRequestBuilder> {
	/**
	 * Organization catalog definition. Module scope applies independently of workspace ACLs. Updates advance every attached collection atomically.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ContentType>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ContentType | undefined>;
	/**
	 * Organization catalog definition. Module scope applies independently of workspace ACLs. Updates advance every attached collection atomically.
	 * @param body Organization catalog definition. Replacement validates all attached collections and advances their schema revisions atomically. Requires contentType.manage, independently of workspace membership.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ContentType>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	put(body: ContentTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ContentType | undefined>;
	/**
	 * Organization catalog definition. Module scope applies independently of workspace ACLs. Updates advance every attached collection atomically.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Organization catalog definition. Module scope applies independently of workspace ACLs. Updates advance every attached collection atomically.
	 * @param body Organization catalog definition. Replacement validates all attached collections and advances their schema revisions atomically. Requires contentType.manage, independently of workspace membership.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: ContentTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/content-types
 */
export interface ContentTypesRequestBuilder extends BaseRequestBuilder<ContentTypesRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.contentTypes.item collection
	 * @param id Unique identifier of the item
	 * @returns {ContentTypesItemRequestBuilder}
	 */
	byId(id: Guid): ContentTypesItemRequestBuilder;
	/**
	 * Read organization content types
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ContentType[]>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ContentType[] | undefined>;
	/**
	 * Requires contentType.manage.
	 * @param body Organization catalog definition. Replacement validates all attached collections and advances their schema revisions atomically. Requires contentType.manage, independently of workspace membership.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ContentType>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: ContentTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ContentType | undefined>;
	/**
	 * Read organization content types
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Requires contentType.manage.
	 * @param body Organization catalog definition. Replacement validates all attached collections and advances their schema revisions atomically. Requires contentType.manage, independently of workspace membership.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ContentTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/fields/{id}
 */
export interface FieldsItemRequestBuilder extends BaseRequestBuilder<FieldsItemRequestBuilder> {
	/**
	 * Requires contentType.manage and the field ETag. Applies atomically to all referencing types and attached collections.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * Requires contentType.manage and the field ETag. Applies atomically to all referencing types and attached collections.
	 * @param body Uses container If-Match. Key, type and scale are immutable. Changes affect future revisions; historical content retains its schema.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<FieldDefinition>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	patch(body: UpdateField, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<FieldDefinition | undefined>;
	/**
	 * Requires contentType.manage and the field ETag. Applies atomically to all referencing types and attached collections.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Requires contentType.manage and the field ETag. Applies atomically to all referencing types and attached collections.
	 * @param body Uses container If-Match. Key, type and scale are immutable. Changes affect future revisions; historical content retains its schema.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPatchRequestInformation(body: UpdateField, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/fields
 */
export interface FieldsRequestBuilder extends BaseRequestBuilder<FieldsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.fields.item collection
	 * @param id Unique identifier of the item
	 * @returns {FieldsItemRequestBuilder}
	 */
	byId(id: Guid): FieldsItemRequestBuilder;
	/**
	 * Requires contentType.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<FieldDefinition[]>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<FieldDefinition[] | undefined>;
	/**
	 * Requires contentType.manage. Attach fields by ID using a catalog content type.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<FieldDefinition>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: CreateCatalogField, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<FieldDefinition | undefined>;
	/**
	 * Requires contentType.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Requires contentType.manage. Attach fields by ID using a catalog content type.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: CreateCatalogField, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface ArchiveRequestBuilder$1 extends BaseRequestBuilder<ArchiveRequestBuilder$1> {
	/**
	 * Requires item write access and list.write scope. Applies immediately to head and published content. Changes the lock version without a content revision. Repeating with the current version is a no-op.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ResourceAccess>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ResourceAccess | undefined>;
	/**
	 * Requires item write access and list.write scope. Applies immediately to head and published content. Changes the lock version without a content revision. Repeating with the current version is a no-op.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/compose-pdf
 */
export interface ComposePdfRequestBuilder extends BaseRequestBuilder<ComposePdfRequestBuilder> {
	/**
	 * Combines the current files of photo items of the item's list or library into one PDF in page order, as an artifact for the item. Each page is turned upright and shrunk so its smallest text keeps target_height, and OCR places the recognized text invisibly on it. An identical request for the same photos reuses an unexpired artifact (reused). The operation fails when a photo changes before the PDF is made. Requires write access to every photo.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(body: ComposePDF, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Combines the current files of photo items of the item's list or library into one PDF in page order, as an artifact for the item. Each page is turned upright and shrunk so its smallest text keeps target_height, and OCR places the recognized text invisibly on it. An identical request for the same photos reuses an unexpired artifact (reused). The operation fails when a photo changes before the PDF is made. Requires write access to every photo.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ComposePDF, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/content/reclaim
 */
export interface ReclaimRequestBuilder extends BaseRequestBuilder<ReclaimRequestBuilder> {
	/**
	 * Asks to free the bytes of a file version of the item after the grace period, such as an original a smaller copy replaced. The version keeps its number, filename, media type, size and checksum. Current, published and staged files defer it, with the reason. Asking again does not move the deadline. Requires write access to the item.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ReclaimedFileVersion>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	post(body: ReclaimFileVersion, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ReclaimedFileVersion | undefined>;
	/**
	 * Asks to free the bytes of a file version of the item after the grace period, such as an original a smaller copy replaced. The version keeps its number, filename, media type, size and checksum. Current, published and staged files defer it, with the reason. Asking again does not move the deadline. Requires write access to the item.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ReclaimFileVersion, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/content/versions/{number}/restore
 */
export interface RestoreRequestBuilder extends BaseRequestBuilder<RestoreRequestBuilder> {
	/**
	 * Requires list.write and document.write, item write access and the current numeric If-Match. Restores file bytes and filename while retaining current metadata. Creates a new blob sequence number and content revision; automatic publishing follows library settings. Explicit publication remains unchanged. Earlier versions remain immutable. The source version must belong to this item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Blob>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Blob | undefined>;
	/**
	 * Requires list.write and document.write, item write access and the current numeric If-Match. Restores file bytes and filename while retaining current metadata. Creates a new blob sequence number and content revision; automatic publishing follows library settings. Explicit publication remains unchanged. Earlier versions remain immutable. The source version must belong to this item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/content/versions/{number}
 */
export interface WithNumberItemRequestBuilder extends BaseRequestBuilder<WithNumberItemRequestBuilder> {
	/**
	 * The restore property
	 */
	get restore(): RestoreRequestBuilder;
}
export interface VersionsGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: FileVersion[] | null;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/content/versions
 */
export interface VersionsRequestBuilder extends BaseRequestBuilder<VersionsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.items.item.content.versions.item collection
	 * @param number Unique identifier of the item
	 * @returns {WithNumberItemRequestBuilder}
	 */
	byNumber(number: number): WithNumberItemRequestBuilder;
	/**
	 * Retained files of a library item, newest first. Draft readers see every version; ordinary readers see only the published file. `is_current` marks the file of the caller's visible revision, `created_by` who uploaded or restored that version, and `availability` whether the bytes are still stored. A reclaimed version keeps its id, version, filename, content type, size and checksum; downloading or restoring it reports that the content is unavailable. Restore a version that still has its bytes with POST /v1/items/{id}/content/versions/{number}/restore. Required module scopes: list.read, document.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<VersionsGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<VersionsRequestBuilderGetQueryParameters> | undefined): Promise<VersionsGetResponse | undefined>;
	/**
	 * Retained files of a library item, newest first. Draft readers see every version; ordinary readers see only the published file. `is_current` marks the file of the caller's visible revision, `created_by` who uploaded or restored that version, and `availability` whether the bytes are still stored. A reclaimed version keeps its id, version, filename, content type, size and checksum; downloading or restoring it reports that the content is unavailable. Restore a version that still has its bytes with POST /v1/items/{id}/content/versions/{number}/restore. Required module scopes: list.read, document.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<VersionsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Retained files of a library item, newest first. Draft readers see every version; ordinary readers see only the published file. `is_current` marks the file of the caller's visible revision, `created_by` who uploaded or restored that version, and `availability` whether the bytes are still stored. A reclaimed version keeps its id, version, filename, content type, size and checksum; downloading or restoring it reports that the content is unavailable. Restore a version that still has its bytes with POST /v1/items/{id}/content/versions/{number}/restore. Required module scopes: list.read, document.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface VersionsRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
interface ContentRequestBuilder$1 extends BaseRequestBuilder<ContentRequestBuilder$1> {
	/**
	 * The reclaim property
	 */
	get reclaim(): ReclaimRequestBuilder;
	/**
	 * The versions property
	 */
	get versions(): VersionsRequestBuilder;
	/**
	 * Ordinary readers can download only the current published blob, including with blob_id. Draft readers default to head and may request retained historical blobs. Required module scopes: list.read and document.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ContentRequestBuilderGetQueryParameters> | undefined): Promise<void>;
	/**
	 * Checks the library duplicate policy in the same transaction as the new file revision. Warn returns duplicates; block returns 409 duplicate_file and stores no revision. Self, deleted items, historical files and inaccessible content do not count. Requires list.write and document.write and item write access with current If-Match. Identical bytes share storage across items and workspaces; file-version metadata and access remain independent. Unreferenced completed objects are periodically collected after a one-hour grace period. Accepts only PDF, TIFF, JPEG, PNG and WebP detected from file signatures; filenames and declared MIME types do not determine stored content_type. Unsupported bytes return 415 without item/revision changes. Size limits apply while streaming.
	 * @param body Binary request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Blob>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	put(body: ArrayBuffer | undefined, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Blob | undefined>;
	/**
	 * Ordinary readers can download only the current published blob, including with blob_id. Draft readers default to head and may request retained historical blobs. Required module scopes: list.read and document.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ContentRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Checks the library duplicate policy in the same transaction as the new file revision. Warn returns duplicates; block returns 409 duplicate_file and stores no revision. Self, deleted items, historical files and inaccessible content do not count. Requires list.write and document.write and item write access with current If-Match. Identical bytes share storage across items and workspaces; file-version metadata and access remain independent. Unreferenced completed objects are periodically collected after a one-hour grace period. Accepts only PDF, TIFF, JPEG, PNG and WebP detected from file signatures; filenames and declared MIME types do not determine stored content_type. Unsupported bytes return 415 without item/revision changes. Size limits apply while streaming.
	 * @param body Binary request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: ArrayBuffer | undefined, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Ordinary readers can download only the current published blob, including with blob_id. Draft readers default to head and may request retained historical blobs. Required module scopes: list.read and document.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface ContentRequestBuilderGetQueryParameters {
	blobId?: Guid;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/integration
 */
export interface IntegrationRequestBuilder extends BaseRequestBuilder<IntegrationRequestBuilder> {
	/**
	 * Read item integration ownership and source-managed field keys
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ItemIntegration>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ItemIntegration | undefined>;
	/**
	 * Read item integration ownership and source-managed field keys
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/move
 */
export interface MoveRequestBuilder extends BaseRequestBuilder<MoveRequestBuilder> {
	/**
	 * Requires list.write, source write access, destination collection read access and destination parent write access. If-Match validates the current item version. Cross-collection moves support content items only, require matching collection kinds and an attached matching content type, and revalidate all current values and references. Identity, blobs, history and relationships survive. Source item grants are removed and the item inherits destination permissions. Search indexes and business keys move atomically; name/unique-value collisions roll back the move. Explicit publications retain frozen snapshots; automatic publishing exposes the head. Historical revisions retain their original schema/container provenance. Workflow run location metadata follows the item. Same-collection moves retain exclusive permissions.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: MoveItem, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 * Requires list.write, source write access, destination collection read access and destination parent write access. If-Match validates the current item version. Cross-collection moves support content items only, require matching collection kinds and an attached matching content type, and revalidate all current values and references. Identity, blobs, history and relationships survive. Source item grants are removed and the item inherits destination permissions. Search indexes and business keys move atomically; name/unique-value collisions roll back the move. Explicit publications retain frozen snapshots; automatic publishing exposes the head. Historical revisions retain their original schema/container provenance. Workflow run location metadata follows the item. Same-collection moves retain exclusive permissions.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: MoveItem, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/optimize-image
 */
export interface OptimizeImageRequestBuilder extends BaseRequestBuilder<OptimizeImageRequestBuilder> {
	/**
	 * Analyzes the text of the item's current JPEG, PNG or WebP image with OCR and makes a smaller WebP copy, as an artifact, whose smallest text keeps the target height. An identical request for the same file version reuses an unexpired artifact (reused). The result gives the reason when the original is kept: the format is not supported, the image is animated, has no reliable text or would not get smaller, or a newer file replaced file_version.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(body: OptimizeImage, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Analyzes the text of the item's current JPEG, PNG or WebP image with OCR and makes a smaller WebP copy, as an artifact, whose smallest text keeps the target height. An identical request for the same file version reuses an unexpired artifact (reused). The result gives the reason when the original is kept: the format is not supported, the image is animated, has no reliable text or would not get smaller, or a newer file replaced file_version.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: OptimizeImage, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export interface PreviewGetResponse extends AdditionalDataHolder, Parsable {
	/**
	 * The blob_id property
	 */
	blobId?: Guid | null;
	/**
	 * The status property
	 */
	status?: UntypedNode | null;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/preview
 */
export interface PreviewRequestBuilder extends BaseRequestBuilder<PreviewRequestBuilder> {
	/**
	 * Requires list.read and document.read, plus resource ACL access. Resolves the visible published/head revision before selecting a rendition. Identical source bytes share a preview; original bytes are never returned. The default file.preview workflow generates previews asynchronously.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<PreviewGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<PreviewRequestBuilderGetQueryParameters> | undefined): Promise<PreviewGetResponse | undefined>;
	/**
	 * Starts rendering a first-page WebP preview, under 100 KiB and 800 pixels, of the item's current file as a background operation; GET /v1/items/{id}/preview serves it. Previews are kept once per file bytes, so bytes rendered before finish at once. Requires document.write and write access to the item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Requires list.read and document.read, plus resource ACL access. Resolves the visible published/head revision before selecting a rendition. Identical source bytes share a preview; original bytes are never returned. The default file.preview workflow generates previews asynchronously.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<PreviewRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Starts rendering a first-page WebP preview, under 100 KiB and 800 pixels, of the item's current file as a background operation; GET /v1/items/{id}/preview serves it. Previews are kept once per file bytes, so bytes rendered before finish at once. Requires document.write and write access to the item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Requires list.read and document.read, plus resource ACL access. Resolves the visible published/head revision before selecting a rendition. Identical source bytes share a preview; original bytes are never returned. The default file.preview workflow generates previews asynchronously.
 */
export interface PreviewRequestBuilderGetQueryParameters {
	/**
	 * An explicit version; ordinary readers can request only their published file.
	 */
	blobId?: Guid;
}
export interface PublicationsGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: Publication[] | null;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/publications
 */
export interface PublicationsRequestBuilder extends BaseRequestBuilder<PublicationsRequestBuilder> {
	/**
	 * Draft readers see lifecycle history. Ordinary readers see publish events for the current published revision only. Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<PublicationsGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<PublicationsRequestBuilderGetQueryParameters> | undefined): Promise<PublicationsGetResponse | undefined>;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Publication>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Publication | undefined>;
	/**
	 * Draft readers see lifecycle history. Ordinary readers see publish events for the current published revision only. Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<PublicationsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Draft readers see lifecycle history. Ordinary readers see publish events for the current published revision only. Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface PublicationsRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/relationships/{linkID}
 */
export interface WithLinkItemRequestBuilder extends BaseRequestBuilder<WithLinkItemRequestBuilder> {
	/**
	 * Either endpoint may remove a directed or symmetric edge. Requires write permission on both endpoints without an ETag precondition. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Relationship changes advance both endpoint item ETags and modification timestamps and emit item.updated for both endpoints. Duplicate creates are no-ops.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * The item must participate in the edge and both endpoints must be visible. Required module scope: list.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RelationshipView>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<RelationshipView | undefined>;
	/**
	 * Either endpoint may patch a directed or symmetric edge. Requires write permission on both endpoints and the edge If-Match. Omitted metadata keys remain; null removes a key. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Relationship changes advance both endpoint item ETags and modification timestamps and emit item.updated for both endpoints. Duplicate creates are no-ops.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Relationship>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 428 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	patch(body: UpdateRelationship, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Relationship | undefined>;
	/**
	 * Either endpoint may remove a directed or symmetric edge. Requires write permission on both endpoints without an ETag precondition. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Relationship changes advance both endpoint item ETags and modification timestamps and emit item.updated for both endpoints. Duplicate creates are no-ops.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * The item must participate in the edge and both endpoints must be visible. Required module scope: list.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Either endpoint may patch a directed or symmetric edge. Requires write permission on both endpoints and the edge If-Match. Omitted metadata keys remain; null removes a key. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Relationship changes advance both endpoint item ETags and modification timestamps and emit item.updated for both endpoints. Duplicate creates are no-ops.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPatchRequestInformation(body: UpdateRelationship, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export type GetDirectionQueryParameterType = (typeof GetDirectionQueryParameterTypeObject)[keyof typeof GetDirectionQueryParameterTypeObject];
export interface RelationshipsGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: RelationshipView[] | null;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/relationships
 */
export interface RelationshipsRequestBuilder extends BaseRequestBuilder<RelationshipsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.items.item.relationships.item collection
	 * @param linkID Unique identifier of the item
	 * @returns {WithLinkItemRequestBuilder}
	 */
	byLinkID(linkID: string): WithLinkItemRequestBuilder;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RelationshipsGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<RelationshipsRequestBuilderGetQueryParameters> | undefined): Promise<RelationshipsGetResponse | undefined>;
	/**
	 * Endpoints may belong to different workspaces. Type is optional; untyped links default to symmetric and allow at most 64 scalar attributes and 16 KiB of JSON. Requires write permission on both endpoints. Ensures one edge for the type and endpoints; repeating creation returns the existing edge without changing metadata, version or audit history, even at a cardinality limit. Symmetric endpoints use canonical ordering. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Relationship changes advance both endpoint item ETags and modification timestamps and emit item.updated for both endpoints. Duplicate creates are no-ops.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Relationship>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: CreateRelationship, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Relationship | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<RelationshipsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Endpoints may belong to different workspaces. Type is optional; untyped links default to symmetric and allow at most 64 scalar attributes and 16 KiB of JSON. Requires write permission on both endpoints. Ensures one edge for the type and endpoints; repeating creation returns the existing edge without changing metadata, version or audit history, even at a cardinality limit. Symmetric endpoints use canonical ordering. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Relationship changes advance both endpoint item ETags and modification timestamps and emit item.updated for both endpoints. Duplicate creates are no-ops.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: CreateRelationship, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface RelationshipsRequestBuilderGetQueryParameters {
	direction?: GetDirectionQueryParameterType;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
	/**
	 * Relationship term ID or label, including synonyms and localized labels.
	 */
	type?: string;
}
declare const GetDirectionQueryParameterTypeObject: {
	readonly Both: "both";
	readonly Outgoing: "outgoing";
	readonly Incoming: "incoming";
};
interface RestoreRequestBuilder$1 extends BaseRequestBuilder<RestoreRequestBuilder$1> {
	/**
	 * Requires item write permission and its current If-Match. Revalidates values against the current schema; removed fields remain removed and current values absent from the snapshot are cleared. Restores the historical content type (which must still be attached), name, keywords and field values, retaining the current blob. Runs ordinary indexing, publication and domain-event logic atomically. Historical revisions are unchanged. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 * Requires item write permission and its current If-Match. Revalidates values against the current schema; removed fields remain removed and current values absent from the snapshot are cleared. Restores the historical content type (which must still be attached), name, keywords and field values, retaining the current blob. Runs ordinary indexing, publication and domain-event logic atomically. Historical revisions are unchanged. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface WithNumberItemRequestBuilder$1 extends BaseRequestBuilder<WithNumberItemRequestBuilder$1> {
	/**
	 * The restore property
	 */
	get restore(): RestoreRequestBuilder$1;
	/**
	 * Requires list.read scope and ordinary read permission on the live item. All retained metadata revisions, including unpublished revisions, are available with their frozen schema. Current head content still requires read_draft; restoring history requires write.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ItemRevision>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ItemRevision | undefined>;
	/**
	 * Requires list.read scope and ordinary read permission on the live item. All retained metadata revisions, including unpublished revisions, are available with their frozen schema. Current head content still requires read_draft; restoring history requires write.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export interface RevisionsGetResponse extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: ItemRevision[] | null;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/revisions
 */
export interface RevisionsRequestBuilder extends BaseRequestBuilder<RevisionsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.items.item.revisions.item collection
	 * @param number Unique identifier of the item
	 * @returns {WithNumberItemRequestBuilder}
	 */
	byNumber(number: number): WithNumberItemRequestBuilder$1;
	/**
	 * Requires list.read scope and ordinary read permission on the live item. All retained metadata revisions, including unpublished revisions, are available with their frozen schema. Current head content still requires read_draft; restoring history requires write. Newest recorded version first. $skiptoken is the last returned recorded number, and following pages contain smaller numbers. revision_number counts recorded entries only; is_current marks the largest retained number, even when recording is disabled.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RevisionsGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<RevisionsRequestBuilderGetQueryParameters> | undefined): Promise<RevisionsGetResponse | undefined>;
	/**
	 * Requires list.read scope and ordinary read permission on the live item. All retained metadata revisions, including unpublished revisions, are available with their frozen schema. Current head content still requires read_draft; restoring history requires write. Newest recorded version first. $skiptoken is the last returned recorded number, and following pages contain smaller numbers. revision_number counts recorded entries only; is_current marks the largest retained number, even when recording is disabled.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<RevisionsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires list.read scope and ordinary read permission on the live item. All retained metadata revisions, including unpublished revisions, are available with their frozen schema. Current head content still requires read_draft; restoring history requires write. Newest recorded version first. $skiptoken is the last returned recorded number, and following pages contain smaller numbers. revision_number counts recorded entries only; is_current marks the largest retained number, even when recording is disabled.
 */
export interface RevisionsRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
export type GetSurfaceQueryParameterType = (typeof GetSurfaceQueryParameterTypeObject)[keyof typeof GetSurfaceQueryParameterTypeObject];
export interface SchemaGetResponse extends Parsable, SchemaRevision {
	/**
	 * The file property
	 */
	file?: SchemaGetResponse_file | null;
	/**
	 * System properties from the same visible revision as the schema.
	 */
	item?: Resource | null;
}
export interface SchemaGetResponse_file extends AdditionalDataHolder, Parsable {
	/**
	 * The content_type property
	 */
	contentType?: string | null;
	/**
	 * The filename property
	 */
	filename?: string | null;
	/**
	 * The size property
	 */
	size?: number | null;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/schema
 */
export interface SchemaRequestBuilder extends BaseRequestBuilder<SchemaRequestBuilder> {
	/**
	 * Returns the frozen content schema of the caller-visible item or folder. Folders use their current metadata schema and do not enforce required fields. Requires list.read and resource read access.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SchemaGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<SchemaRequestBuilderGetQueryParameters> | undefined): Promise<SchemaGetResponse | undefined>;
	/**
	 * Returns the frozen content schema of the caller-visible item or folder. Folders use their current metadata schema and do not enforce required fields. Requires list.read and resource read access.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<SchemaRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Returns the frozen content schema of the caller-visible item or folder. Folders use their current metadata schema and do not enforce required fields. Requires list.read and resource read access.
 */
export interface SchemaRequestBuilderGetQueryParameters {
	/**
	 * Auto chooses head for draft readers and published for ordinary readers. Head requires draft access.
	 */
	surface?: GetSurfaceQueryParameterType;
}
declare const GetSurfaceQueryParameterTypeObject: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
/**
 * Builds and executes requests for operations under /v1/items/{id}/search/chunks
 */
export interface ChunksRequestBuilder extends BaseRequestBuilder<ChunksRequestBuilder> {
	/**
	 * Splits text read from the item's file, or text given directly, into chunks and stages them in the search store under a generation, which POST /v1/search/generations/{id}/publish makes searchable. A new generation is identified by this operation's ID. Requires document.write and write access to the item. 422 when the deployment has no search store.
	 * @param body What text to chunk and how. Give text_id or text.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(body: ChunkText, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Splits text read from the item's file, or text given directly, into chunks and stages them in the search store under a generation, which POST /v1/search/generations/{id}/publish makes searchable. A new generation is identified by this operation's ID. Requires document.write and write access to the item. 422 when the deployment has no search store.
	 * @param body What text to chunk and how. Give text_id or text.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ChunkText, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/search
 */
export interface SearchRequestBuilder extends BaseRequestBuilder<SearchRequestBuilder> {
	/**
	 * The chunks property
	 */
	get chunks(): ChunksRequestBuilder;
}
export type GetKindQueryParameterType = (typeof GetKindQueryParameterTypeObject)[keyof typeof GetKindQueryParameterTypeObject];
export type GetStatusQueryParameterType = (typeof GetStatusQueryParameterTypeObject)[keyof typeof GetStatusQueryParameterTypeObject];
/**
 * Builds and executes requests for operations under /v1/items/{id}/tasks
 */
export interface TasksRequestBuilder extends BaseRequestBuilder<TasksRequestBuilder> {
	/**
	 * The tasks about an item that the caller may see.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TaskList>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TasksRequestBuilderGetQueryParameters> | undefined): Promise<TaskList | undefined>;
	/**
	 * The tasks about an item that the caller may see.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TasksRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * The tasks about an item that the caller may see.
 */
export interface TasksRequestBuilderGetQueryParameters {
	key?: string;
	kind?: GetKindQueryParameterType;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	status?: GetStatusQueryParameterType;
	top?: number;
}
declare const GetKindQueryParameterTypeObject: {
	readonly Task: "task";
	readonly Approval: "approval";
};
declare const GetStatusQueryParameterTypeObject: {
	readonly Open: "open";
	readonly Completed: "completed";
	readonly Cancelled: "cancelled";
	readonly Expired: "expired";
};
/**
 * Builds and executes requests for operations under /v1/items/{id}/text
 */
export interface TextRequestBuilder extends BaseRequestBuilder<TextRequestBuilder> {
	/**
	 * The page texts a workflow with document.read_text read from the file version the caller may read: the current one, or blob_id. 404 text_not_read before it was read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ItemText>}
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TextRequestBuilderGetQueryParameters> | undefined): Promise<ItemText | undefined>;
	/**
	 * Starts reading the text of the item's current file as a background operation: the text layer of PDF pages, and OCR of pages without one and of images when the deployment has OCR. The operation reads the file version that is current now, even if the item gets another file meanwhile. Text already read from the same bytes with the same settings is reused. Requires document.write and write access to the item. When the operation succeeds, its result is a ReadTextResult and GET /v1/items/{id}/text returns the pages. A file whose text cannot be read (another format, a protected or broken PDF) fails the operation. The body is optional. 422 refuses ocr=always without an OCR engine.
	 * @param body How the text is read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: ReadText, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * The page texts a workflow with document.read_text read from the file version the caller may read: the current one, or blob_id. 404 text_not_read before it was read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TextRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Starts reading the text of the item's current file as a background operation: the text layer of PDF pages, and OCR of pages without one and of images when the deployment has OCR. The operation reads the file version that is current now, even if the item gets another file meanwhile. Text already read from the same bytes with the same settings is reused. Requires document.write and write access to the item. When the operation succeeds, its result is a ReadTextResult and GET /v1/items/{id}/text returns the pages. A file whose text cannot be read (another format, a protected or broken PDF) fails the operation. The body is optional. 422 refuses ocr=always without an OCR engine.
	 * @param body How the text is read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ReadText, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * The page texts a workflow with document.read_text read from the file version the caller may read: the current one, or blob_id. 404 text_not_read before it was read.
 */
export interface TextRequestBuilderGetQueryParameters {
	blobId?: string;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/unarchive
 */
export interface UnarchiveRequestBuilder extends BaseRequestBuilder<UnarchiveRequestBuilder> {
	/**
	 * Requires item write access and list.write scope. Applies immediately to head and published content. Changes the lock version without a content revision. Repeating with the current version is a no-op.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ResourceAccess>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ResourceAccess | undefined>;
	/**
	 * Requires item write access and list.write scope. Applies immediately to head and published content. Changes the lock version without a content revision. Repeating with the current version is a no-op.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}/unpublish
 */
export interface UnpublishRequestBuilder extends BaseRequestBuilder<UnpublishRequestBuilder> {
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/items/{id}
 */
export interface ItemsItemRequestBuilder extends BaseRequestBuilder<ItemsItemRequestBuilder> {
	/**
	 * The archive property
	 */
	get archive(): ArchiveRequestBuilder$1;
	/**
	 * The composePdf property
	 */
	get composePdf(): ComposePdfRequestBuilder;
	/**
	 * The content property
	 */
	get content(): ContentRequestBuilder$1;
	/**
	 * The integration property
	 */
	get integration(): IntegrationRequestBuilder;
	/**
	 * The move property
	 */
	get move(): MoveRequestBuilder;
	/**
	 * The optimizeImage property
	 */
	get optimizeImage(): OptimizeImageRequestBuilder;
	/**
	 * The preview property
	 */
	get preview(): PreviewRequestBuilder;
	/**
	 * The publications property
	 */
	get publications(): PublicationsRequestBuilder;
	/**
	 * The relationships property
	 */
	get relationships(): RelationshipsRequestBuilder;
	/**
	 * The revisions property
	 */
	get revisions(): RevisionsRequestBuilder;
	/**
	 * The schema property
	 */
	get schema(): SchemaRequestBuilder;
	/**
	 * The search property
	 */
	get search(): SearchRequestBuilder;
	/**
	 * The tasks property
	 */
	get tasks(): TasksRequestBuilder;
	/**
	 * The text property
	 */
	get text(): TextRequestBuilder;
	/**
	 * The unarchive property
	 */
	get unarchive(): UnarchiveRequestBuilder;
	/**
	 * The unpublish property
	 */
	get unpublish(): UnpublishRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/items
 */
export interface ItemsRequestBuilder extends BaseRequestBuilder<ItemsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.items.item collection
	 * @param id Unique identifier of the item
	 * @returns {ItemsItemRequestBuilder}
	 */
	byId(id: Guid): ItemsItemRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/keywords/{id}/promote
 */
export interface PromoteRequestBuilder extends BaseRequestBuilder<PromoteRequestBuilder> {
	/**
	 * Moves a keyword into a managed set, available as a keyword, or merges it into the set's term with the same name, label or synonym. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Term>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: PromoteKeyword, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Term | undefined>;
	/**
	 * Moves a keyword into a managed set, available as a keyword, or merges it into the set's term with the same name, label or synonym. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: PromoteKeyword, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/keywords/{id}
 */
export interface KeywordsItemRequestBuilder extends BaseRequestBuilder<KeywordsItemRequestBuilder> {
	/**
	 * The promote property
	 */
	get promote(): PromoteRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/keywords/popular
 */
export interface PopularRequestBuilder extends BaseRequestBuilder<PopularRequestBuilder> {
	/**
	 * Requires taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<KeywordCountList>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<PopularRequestBuilderGetQueryParameters> | undefined): Promise<KeywordCountList | undefined>;
	/**
	 * Requires taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<PopularRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
 */
export interface PopularRequestBuilderGetQueryParameters {
	/**
	 * At most 100.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/keywords
 */
export interface KeywordsRequestBuilder extends BaseRequestBuilder<KeywordsRequestBuilder> {
	/**
	 * The popular property
	 */
	get popular(): PopularRequestBuilder;
	/**
	 * Gets an item from the PaperGo.v1.keywords.item collection
	 * @param id Unique identifier of the item
	 * @returns {KeywordsItemRequestBuilder}
	 */
	byId(id: string): KeywordsItemRequestBuilder;
	/**
	 * Keywords of the keywords set and terms available as keywords; names starting with q first. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermList>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<KeywordsRequestBuilderGetQueryParameters> | undefined): Promise<TermList | undefined>;
	/**
	 * Returns the keyword with this name or synonym, or adds it to the keywords set. Requires taxonomy.read. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Term>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: KeywordInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Term | undefined>;
	/**
	 * Keywords of the keywords set and terms available as keywords; names starting with q first. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<KeywordsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Returns the keyword with this name or synonym, or adds it to the keywords set. Requires taxonomy.read. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: KeywordInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Keywords of the keywords set and terms available as keywords; names starting with q first. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
 */
export interface KeywordsRequestBuilderGetQueryParameters {
	/**
	 * Search names, labels and synonyms.
	 */
	q?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/operations/{id}/cancel
 */
export interface CancelRequestBuilder extends BaseRequestBuilder<CancelRequestBuilder> {
	/**
	 * Ends a pending operation for its creator or an organization manager. Work in progress is discarded: a running execution cannot publish its result, and work shared with other operations continues for them. Index maintenance cannot be cancelled.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Ends a pending operation for its creator or an organization manager. Work in progress is discarded: a running execution cannot publish its result, and work shared with other operations continues for them. Index maintenance cannot be cancelled.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/operations/{id}
 */
export interface OperationsItemRequestBuilder extends BaseRequestBuilder<OperationsItemRequestBuilder> {
	/**
	 * The cancel property
	 */
	get cancel(): CancelRequestBuilder;
	/**
	 * Tracked field_index or bulk_update background operation. Indexing converges one field in short batches; later field changes supersede it. Filtered updates commit each item independently, survive restarts, and report failures without rolling back successful edits. Bulk details require the starter and collection read access. Terminal records and results expire 30 days after their last update; pending work survives restarts. Collection index operations are readable with collection access; other kinds require the initiating user. Organization operations additionally require organization.manage.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Tracked field_index or bulk_update background operation. Indexing converges one field in short batches; later field changes supersede it. Filtered updates commit each item independently, survive restarts, and report failures without rolling back successful edits. Bulk details require the starter and collection read access. Terminal records and results expire 30 days after their last update; pending work survives restarts. Collection index operations are readable with collection access; other kinds require the initiating user. Organization operations additionally require organization.manage.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/operations
 */
export interface OperationsRequestBuilder extends BaseRequestBuilder<OperationsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.operations.item collection
	 * @param id Unique identifier of the item
	 * @returns {OperationsItemRequestBuilder}
	 */
	byId(id: string): OperationsItemRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/people
 */
export interface PeopleRequestBuilder extends BaseRequestBuilder<PeopleRequestBuilder> {
	/**
	 * Requires list.read. IDs are enrolled organization subjects; selecting a user grants no content access.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<string[]>}
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<string[] | undefined>;
	/**
	 * Requires list.read. IDs are enrolled organization subjects; selecting a user grants no content access.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/relationship-types/{id}
 */
export interface RelationshipTypesItemRequestBuilder extends BaseRequestBuilder<RelationshipTypesItemRequestBuilder> {
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RelationshipType>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<RelationshipType | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/relationship-types
 */
export interface RelationshipTypesRequestBuilder extends BaseRequestBuilder<RelationshipTypesRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.relationshipTypes.item collection
	 * @param id Unique identifier of the item
	 * @returns {RelationshipTypesItemRequestBuilder}
	 */
	byId(id: string): RelationshipTypesItemRequestBuilder;
	/**
	 * Organization taxonomy predicates; requires list.read and taxonomy.read to discover, list.write and taxonomy.read to ensure an immutable policy. Names, translations and synonyms are curated through taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RelationshipTypePage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<RelationshipTypesRequestBuilderGetQueryParameters> | undefined): Promise<RelationshipTypePage | undefined>;
	/**
	 * Organization taxonomy predicates; requires list.read and taxonomy.read to discover, list.write and taxonomy.read to ensure an immutable policy. Names, translations and synonyms are curated through taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RelationshipType>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: RelationshipTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<RelationshipType | undefined>;
	/**
	 * Organization taxonomy predicates; requires list.read and taxonomy.read to discover, list.write and taxonomy.read to ensure an immutable policy. Names, translations and synonyms are curated through taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<RelationshipTypesRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Organization taxonomy predicates; requires list.read and taxonomy.read to discover, list.write and taxonomy.read to ensure an immutable policy. Names, translations and synonyms are curated through taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: RelationshipTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Organization taxonomy predicates; requires list.read and taxonomy.read to discover, list.write and taxonomy.read to ensure an immutable policy. Names, translations and synonyms are curated through taxonomy.
 */
export interface RelationshipTypesRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/bulk
 */
export interface BulkRequestBuilder extends BaseRequestBuilder<BulkRequestBuilder> {
	/**
	 * 1..100 item creates, updates/moves, publishes, unpublishes or deletes in one list/library and one transaction. Requires collection read and per-operation write/publish permissions. Existing items require a positive current version in the body and may occur only once. No collection If-Match is required. Existing folders support metadata updates, same-collection moves and empty-folder deletion; publishing and creates target items. Blob transfers are excluded. Create parent_id defaults to the collection; all targets and destinations stay in it. All revisions, projections, relationships and audit effects roll back on any failure, which reports operation_index when applicable. No retry deduplication. Request body limit: 1 MiB.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<BulkResponse>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 * @throws {Problem} error when the service returns a 503 status code
	 */
	post(body: BulkRequest, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<BulkResponse | undefined>;
	/**
	 * 1..100 item creates, updates/moves, publishes, unpublishes or deletes in one list/library and one transaction. Requires collection read and per-operation write/publish permissions. Existing items require a positive current version in the body and may occur only once. No collection If-Match is required. Existing folders support metadata updates, same-collection moves and empty-folder deletion; publishing and creates target items. Blob transfers are excluded. Create parent_id defaults to the collection; all targets and destinations stay in it. All revisions, projections, relationships and audit effects roll back on any failure, which reports operation_index when applicable. No retry deduplication. Request body limit: 1 MiB.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: BulkRequest, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/bulk-update
 */
export interface BulkUpdateRequestBuilder extends BaseRequestBuilder<BulkUpdateRequestBuilder> {
	/**
	 * Starts a durable filtered edit of current head content. Requires list.read, list.write and collection write access. The existing typed query selects readable drafts; omitted query selects all readable head items and folders, including nested content. Sort and group_by are rejected. Selection freezes when execution begins. Each item merges values (null removes a value) through current validation, permissions, uniqueness, revision, audit and event handling, in its own commit. Current name/keywords can also be changed. Successful items survive other items' failures. Poll Location for matched, processed, updated, failed, progress and up to 50 failure reasons; failed counts all failures. succeeded means execution finished, including partial success. Access, schema or infrastructure failures stop the operation with status failed. Cancellation on shutdown leaves pending work resumable. Bulk operations and failure details are visible only to their starter, with collection read access. The explicit /bulk endpoint remains an atomic transaction of up to 100 actions.
	 * @param body At least one of name, keywords or non-empty values is required. Query is optional and uses head content.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 * @throws {Problem} error when the service returns a 503 status code
	 */
	post(body: FilteredBulkUpdate, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Starts a durable filtered edit of current head content. Requires list.read, list.write and collection write access. The existing typed query selects readable drafts; omitted query selects all readable head items and folders, including nested content. Sort and group_by are rejected. Selection freezes when execution begins. Each item merges values (null removes a value) through current validation, permissions, uniqueness, revision, audit and event handling, in its own commit. Current name/keywords can also be changed. Successful items survive other items' failures. Poll Location for matched, processed, updated, failed, progress and up to 50 failure reasons; failed counts all failures. succeeded means execution finished, including partial success. Access, schema or infrastructure failures stop the operation with status failed. Cancellation on shutdown leaves pending work resumable. Bulk operations and failure details are visible only to their starter, with collection read access. The explicit /bulk endpoint remains an atomic transaction of up to 100 actions.
	 * @param body At least one of name, keywords or non-empty values is required. Query is optional and uses head content.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: FilteredBulkUpdate, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/calendar/export
 */
export interface ExportRequestBuilder extends BaseRequestBuilder<ExportRequestBuilder> {
	/**
	 * Export visible Event items as iCalendar
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ArrayBuffer>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ExportRequestBuilderGetQueryParameters> | undefined): Promise<ArrayBuffer | undefined>;
	/**
	 * Export visible Event items as iCalendar
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ExportRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Export visible Event items as iCalendar
 */
export interface ExportRequestBuilderGetQueryParameters {
	surface?: GetSurfaceQueryParameterType$1;
}
type GetSurfaceQueryParameterType$1 = (typeof GetSurfaceQueryParameterTypeObject$1)[keyof typeof GetSurfaceQueryParameterTypeObject$1];
declare const GetSurfaceQueryParameterTypeObject$1: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
/**
 * Builds and executes requests for operations under /v1/resources/{id}/calendar/occurrences
 */
export interface OccurrencesRequestBuilder extends BaseRequestBuilder<OccurrencesRequestBuilder> {
	/**
	 * Expands Event content in a range of at most 93 days, up to 10000 series and 10000 occurrences. Uses ordinary field filters, list permissions and head/published selection. Does not truncate on limits.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<CalendarOccurrence[]>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: CalendarQuery, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<CalendarOccurrence[] | undefined>;
	/**
	 * Expands Event content in a range of at most 93 days, up to 10000 series and 10000 occurrences. Uses ordinary field filters, list permissions and head/published selection. Does not truncate on limits.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: CalendarQuery, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/calendar
 */
export interface CalendarRequestBuilder extends BaseRequestBuilder<CalendarRequestBuilder> {
	/**
	 * The export property
	 */
	get exportEscaped(): ExportRequestBuilder;
	/**
	 * The occurrences property
	 */
	get occurrences(): OccurrencesRequestBuilder;
}
export interface ChildrenGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The search text matched more than 10,000 entries; the page covers only the first 10,000 matches. Narrow the search.
	 */
	searchCapped?: boolean | null;
	/**
	 * The value property
	 */
	value?: Resource[] | null;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/children
 */
export interface ChildrenRequestBuilder extends BaseRequestBuilder<ChildrenRequestBuilder> {
	/**
	 * Browse visible direct children
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ChildrenGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ChildrenRequestBuilderGetQueryParameters> | undefined): Promise<ChildrenGetResponse | undefined>;
	/**
	 * First create returns 201. Retrying an item/folder create with the same caller ID returns the current readable live entry unchanged with 200 and its current ETag, regardless of new body fields. Requires list.write module scope; an existing retry requires read access rather than create ACL access. Conflicting IDs, including deleted, different-collection and inaccessible entries, return 409 without content. All retry checks and the initial insertion are serialized in one write transaction; retries append no revisions, audit or events.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: CreateResource, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 * Browse visible direct children
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ChildrenRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * First create returns 201. Retrying an item/folder create with the same caller ID returns the current readable live entry unchanged with 200 and its current ETag, regardless of new body fields. Requires list.write module scope; an existing retry requires read access rather than create ACL access. Conflicting IDs, including deleted, different-collection and inaccessible entries, return 409 without content. All retry checks and the initial insertion are serialized in one write transaction; retries append no revisions, audit or events.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: CreateResource, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Browse visible direct children
 */
export interface ChildrenRequestBuilderGetQueryParameters {
	archive?: ArchiveSelection;
	/**
	 * Opt-in indexed field key; requires list/library/folder parent scope.
	 */
	filterField?: string;
	/**
	 * Typed comparison. Boolean supports eq only.
	 */
	filterOp?: GetFilter_opQueryParameterType;
	/**
	 * Typed value, parsed without floating-point conversion for integer and decimal fields.
	 */
	filterValue?: string;
	/**
	 * Shared keyword term ID or label; includes intrinsic keywords and keywords fields.
	 */
	keyword?: string;
	/**
	 * Only resources of this kind, e.g. kind=folder for the folders of a large library.
	 */
	kind?: GetKindQueryParameterType$1;
	q?: string;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Auto chooses head for draft readers and published for ordinary readers. Head requires draft access.
	 */
	surface?: GetSurfaceQueryParameterType$2;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
export type GetFilter_opQueryParameterType = (typeof GetFilter_opQueryParameterTypeObject)[keyof typeof GetFilter_opQueryParameterTypeObject];
type GetKindQueryParameterType$1 = (typeof GetKindQueryParameterTypeObject$1)[keyof typeof GetKindQueryParameterTypeObject$1];
type GetSurfaceQueryParameterType$2 = (typeof GetSurfaceQueryParameterTypeObject$2)[keyof typeof GetSurfaceQueryParameterTypeObject$2];
declare const GetFilter_opQueryParameterTypeObject: {
	readonly Eq: "eq";
	readonly Gt: "gt";
	readonly Gte: "gte";
	readonly Lt: "lt";
	readonly Lte: "lte";
};
declare const GetKindQueryParameterTypeObject$1: {
	readonly Workspace: "workspace";
	readonly List: "list";
	readonly Library: "library";
	readonly Folder: "folder";
	readonly Item: "item";
};
declare const GetSurfaceQueryParameterTypeObject$2: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
/**
 * Builds and executes requests for operations under /v1/resources/{id}/content-types/{typeID}
 */
export interface WithTypeItemRequestBuilder extends BaseRequestBuilder<WithTypeItemRequestBuilder> {
	/**
	 * Requires collection manage access and the current collection If-Match. Rejects the last attachment, a type used by live items, and dependent saved views. Chooses a remaining default when necessary. Updates the effective schema and business keys atomically; organization catalog types and frozen revisions remain. Deleted items with a detached type require reattachment before restoration.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * Requires collection manage access and the current collection If-Match. Rejects the last attachment, a type used by live items, and dependent saved views. Chooses a remaining default when necessary. Updates the effective schema and business keys atomically; organization catalog types and frozen revisions remain. Deleted items with a detached type require reattachment before restoration.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface ContentTypesRequestBuilder$1 extends BaseRequestBuilder<ContentTypesRequestBuilder$1> {
	/**
	 * Gets an item from the PaperGo.v1.resources.item.contentTypes.item collection
	 * @param typeID Unique identifier of the item
	 * @returns {WithTypeItemRequestBuilder}
	 */
	byTypeID(typeID: Guid): WithTypeItemRequestBuilder;
	/**
	 *  Required module scopes: contentType.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<CollectionContentType[]>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<CollectionContentType[] | undefined>;
	/**
	 * Requires list.read, contentType.read and manage ACL on the collection. The shared definition is not copied. Returns the collection and its new ETag.
	 * @param body Attach the organization definition by ID without copying. At most 32 types per collection. Selecting a default affects only this collection. Conflicting field keys are rejected; types that share a key must reference the same catalog field ID.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: AttachContentTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 *  Required module scopes: contentType.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Requires list.read, contentType.read and manage ACL on the collection. The shared definition is not copied. Returns the collection and its new ETag.
	 * @param body Attach the organization definition by ID without copying. At most 32 types per collection. Selecting a default affects only this collection. Conflicting field keys are rejected; types that share a key must reference the same catalog field ID.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: AttachContentTypeInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/delta
 */
export interface DeltaRequestBuilder extends BaseRequestBuilder<DeltaRequestBuilder> {
	/**
	 * Initial synchronization returns visible items and folders. Follow @odata.nextLink, then persist @odata.deltaLink to retrieve subsequent edits, deletions, moves and access changes. Tokens are bound to caller and collection, durable for 30 days; page continuations expire after five minutes. 410 requires a new initial synchronization. Each synchronization scans the current visible collection and compares stored fingerprints; transient changes that return to the same state between polls are coalesced. Required module scope: list.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<DeltaPage>}
	 * @throws {Problem} error when the service returns a 410 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<DeltaRequestBuilderGetQueryParameters> | undefined): Promise<DeltaPage | undefined>;
	/**
	 * Initial synchronization returns visible items and folders. Follow @odata.nextLink, then persist @odata.deltaLink to retrieve subsequent edits, deletions, moves and access changes. Tokens are bound to caller and collection, durable for 30 days; page continuations expire after five minutes. 410 requires a new initial synchronization. Each synchronization scans the current visible collection and compares stored fingerprints; transient changes that return to the same state between polls are coalesced. Required module scope: list.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<DeltaRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Initial synchronization returns visible items and folders. Follow @odata.nextLink, then persist @odata.deltaLink to retrieve subsequent edits, deletions, moves and access changes. Tokens are bound to caller and collection, durable for 30 days; page continuations expire after five minutes. 410 requires a new initial synchronization. Each synchronization scans the current visible collection and compares stored fingerprints; transient changes that return to the same state between polls are coalesced. Required module scope: list.read.
 */
export interface DeltaRequestBuilderGetQueryParameters {
	/**
	 * Opaque token supplied by @odata.deltaLink; mutually exclusive with $skiptoken.
	 */
	deltatoken?: string;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/documents
 */
export interface DocumentsRequestBuilder extends BaseRequestBuilder<DocumentsRequestBuilder> {
	/**
	 * Requires document.write, list.read and list.write and write access to the library and selected folder. One file part is required; one metadata JSON form field is optional. Either order is accepted. File bytes stream under the configured upload limit; metadata is limited to 64 KiB and request framing to an additional 128 KiB. Metadata, first file version, first revision, indexes, publication and events commit together. Invalid metadata, malformed multipart, duplicate blocking and failed writes create no item and clean up stored bytes. Returns 201 with the new item, file, duplicate warnings, Location and numeric ETag. Identical bytes share storage across items and workspaces; file-version metadata and access remain independent. Unreferenced completed objects are periodically collected after a one-hour grace period. Accepts only PDF, TIFF, JPEG, PNG and WebP detected from file signatures; filenames and declared MIME types do not determine stored content_type. Unsupported bytes return 415 without item/revision changes. Size limits apply while streaming.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<DocumentUpload>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: MultipartBody, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<DocumentUpload | undefined>;
	/**
	 * Requires document.write, list.read and list.write and write access to the library and selected folder. One file part is required; one metadata JSON form field is optional. Either order is accepted. File bytes stream under the configured upload limit; metadata is limited to 64 KiB and request framing to an additional 128 KiB. Metadata, first file version, first revision, indexes, publication and events commit together. Invalid metadata, malformed multipart, duplicate blocking and failed writes create no item and clean up stored bytes. Returns 201 with the new item, file, duplicate warnings, Location and numeric ETag. Identical bytes share storage across items and workspaces; file-version metadata and access remain independent. Unreferenced completed objects are periodically collected after a one-hour grace period. Accepts only PDF, TIFF, JPEG, PNG and WebP detected from file signatures; filenames and declared MIME types do not determine stored content_type. Unsupported bytes return 415 without item/revision changes. Size limits apply while streaming.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: MultipartBody, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/fields/{fieldID}
 */
export interface WithFieldItemRequestBuilder extends BaseRequestBuilder<WithFieldItemRequestBuilder> {
	/**
	 * Uses the collection ETag. Rejects dependencies in rules and saved views. Removes memberships, query indexes and business key claims; immutable revisions retain their original fields and values. Removed keys cannot be reused. Later edits prune carried values, while explicitly supplied patch keys reject removed keys. Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param body Uses container If-Match. Key, type and scale are immutable. Changes affect future revisions; historical content retains its schema.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<FieldDefinition>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	patch(body: UpdateField, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<FieldDefinition | undefined>;
	/**
	 * Uses the collection ETag. Rejects dependencies in rules and saved views. Removes memberships, query indexes and business key claims; immutable revisions retain their original fields and values. Removed keys cannot be reused. Later edits prune carried values, while explicitly supplied patch keys reject removed keys. Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param body Uses container If-Match. Key, type and scale are immutable. Changes affect future revisions; historical content retains its schema.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPatchRequestInformation(body: UpdateField, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export interface FieldsGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: FieldDefinition[] | null;
}
interface FieldsRequestBuilder$1 extends BaseRequestBuilder<FieldsRequestBuilder$1> {
	/**
	 * Gets an item from the PaperGo.v1.resources.item.fields.item collection
	 * @param fieldID Unique identifier of the item
	 * @returns {WithFieldItemRequestBuilder}
	 */
	byFieldID(fieldID: Guid): WithFieldItemRequestBuilder;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<FieldsGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<FieldsGetResponse | undefined>;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<FieldDefinition>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: CreateField, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<FieldDefinition | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply. Field definitions are organization-wide; editing through a collection applies to every collection using them.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: CreateField, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/integrations/{integrationID}/refresh
 */
export interface RefreshRequestBuilder extends BaseRequestBuilder<RefreshRequestBuilder> {
	/**
	 * Requires list Manage and list.write; paused integrations must be resumed. The durable operation carries only the integration ID.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Requires list Manage and list.write; paused integrations must be resumed. The durable operation carries only the integration ID.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/integrations/{integrationID}
 */
export interface WithIntegrationItemRequestBuilder extends BaseRequestBuilder<WithIntegrationItemRequestBuilder> {
	/**
	 * The refresh property
	 */
	get refresh(): RefreshRequestBuilder;
	/**
	 * Remove configuration, retaining its items as editable local content
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * Rename, pause, resume or replace integration configuration
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ListIntegration>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	patch(body: UpdateListIntegration, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ListIntegration | undefined>;
	/**
	 * Remove configuration, retaining its items as editable local content
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Rename, pause, resume or replace integration configuration
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPatchRequestInformation(body: UpdateListIntegration, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/integrations
 */
export interface IntegrationsRequestBuilder extends BaseRequestBuilder<IntegrationsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.resources.item.integrations.item collection
	 * @param integrationID Unique identifier of the item
	 * @returns {WithIntegrationItemRequestBuilder}
	 */
	byIntegrationID(integrationID: Guid): WithIntegrationItemRequestBuilder;
	/**
	 * Requires list read access. Private configuration, provider state and credentials are omitted.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ListIntegration[]>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ListIntegration[] | undefined>;
	/**
	 * Requires list Manage access and list.read/list.write scopes. Automatically attaches the Event content type without requiring contentType.read, queues a first refresh, and activates the workspace refresh workflow once. Same-list duplicate URLs are rejected; separate lists and sources have independent identities.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ListIntegration>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: CreateListIntegration, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ListIntegration | undefined>;
	/**
	 * Requires list read access. Private configuration, provider state and credentials are omitted.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Requires list Manage access and list.read/list.write scopes. Automatically attaches the Event content type without requiring contentType.read, queues a first refresh, and activates the workspace refresh workflow once. Same-list duplicate URLs are rejected; separate lists and sources have independent identities.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: CreateListIntegration, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export interface MoveSelection extends AdditionalDataHolder, Parsable {
	/**
	 * The id property
	 */
	id?: string | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
export interface MoveSelectionPostRequestBody extends AdditionalDataHolder, Parsable {
	/**
	 * The items property
	 */
	items?: MoveSelectionPostRequestBody_items[] | null;
	/**
	 * Destination folder; empty selects collection root.
	 */
	parentId?: string | null;
}
export interface MoveSelectionPostRequestBody_items extends AdditionalDataHolder, Parsable {
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The version property
	 */
	version?: number | null;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/move-selection
 */
export interface MoveSelectionRequestBuilder extends BaseRequestBuilder<MoveSelectionRequestBuilder> {
	/**
	 * Requires write on each selected resource and destination. Moves 1–100 top-level entries, with each current version in the body. Ancestor/descendant selections, cycles and cross-collection moves are rejected. Every move rolls back on failure. Folder contents remain in their folder. A name_conflict reports the supplied source_item_id; existing_item is included only for a writable replacement file.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<MoveSelection[]>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: MoveSelectionPostRequestBody, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<MoveSelection[] | undefined>;
	/**
	 * Requires write on each selected resource and destination. Moves 1–100 top-level entries, with each current version in the body. Ancestor/descendant selections, cycles and cross-collection moves are rejected. Every move rolls back on failure. Folder contents remain in their folder. A name_conflict reports the supplied source_item_id; existing_item is included only for a writable replacement file.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: MoveSelectionPostRequestBody, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface OperationsRequestBuilder$1 extends BaseRequestBuilder<OperationsRequestBuilder$1> {
	/**
	 * Tracked field_index or bulk_update background operation. Indexing converges one field in short batches; later field changes supersede it. Filtered updates commit each item independently, survive restarts, and report failures without rolling back successful edits. Bulk details require the starter and collection read access. Terminal records and results expire 30 days after their last update; pending work survives restarts. Collection index operations are readable with collection access; other kinds require the initiating user. Organization operations additionally require organization.manage.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<OperationPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<OperationsRequestBuilderGetQueryParameters> | undefined): Promise<OperationPage | undefined>;
	/**
	 * Tracked field_index or bulk_update background operation. Indexing converges one field in short batches; later field changes supersede it. Filtered updates commit each item independently, survive restarts, and report failures without rolling back successful edits. Bulk details require the starter and collection read access. Terminal records and results expire 30 days after their last update; pending work survives restarts. Collection index operations are readable with collection access; other kinds require the initiating user. Organization operations additionally require organization.manage.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<OperationsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Tracked field_index or bulk_update background operation. Indexing converges one field in short batches; later field changes supersede it. Filtered updates commit each item independently, survive restarts, and report failures without rolling back successful edits. Bulk details require the starter and collection read access. Terminal records and results expire 30 days after their last update; pending work survives restarts. Collection index operations are readable with collection access; other kinds require the initiating user. Organization operations additionally require organization.manage.
 */
export interface OperationsRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/permissions
 */
export interface PermissionsRequestBuilder extends BaseRequestBuilder<PermissionsRequestBuilder> {
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Permissions>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Permissions | undefined>;
	/**
	 * Break, copy or reset inheritance using typed user, group and workspace role principals. Copied group grants retain group IDs and resolve current nested memberships. Copying workspace inheritance retains dynamic visitor/member/owner role principals. Workspace owners and workspace.manage holders retain control. A permission change that removes the caller's manage access rolls back. list.read and resource manage access are required. Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply. ACL changes deliver permissions.changed (action=replace) to the resource and every item and folder that inherits from it, with previous_scope_id and current_scope_id; the root event, with previous/current permission snapshots (inherit, grants, scope_id, effective_grants), commits with the write and audit, and the dispatcher delivers it in batches. Unchanged ACLs raise no event.
	 * @param body SharePoint-style exclusive scopes. An inherited resource has no local grants. Grants at the nearest exclusive ancestor apply; ancestors beyond it do not. Grants within a scope are additive. Reset with inherit=true and no grants. Denies are rejected.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	put(body: Permissions, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Break, copy or reset inheritance using typed user, group and workspace role principals. Copied group grants retain group IDs and resolve current nested memberships. Copying workspace inheritance retains dynamic visitor/member/owner role principals. Workspace owners and workspace.manage holders retain control. A permission change that removes the caller's manage access rolls back. list.read and resource manage access are required. Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply. ACL changes deliver permissions.changed (action=replace) to the resource and every item and folder that inherits from it, with previous_scope_id and current_scope_id; the root event, with previous/current permission snapshots (inherit, grants, scope_id, effective_grants), commits with the write and audit, and the dispatcher delivers it in batches. Unchanged ACLs raise no event.
	 * @param body SharePoint-style exclusive scopes. An inherited resource has no local grants. Grants at the nearest exclusive ancestor apply; ancestors beyond it do not. Grants within a scope are additive. Reset with inherit=true and no grants. Denies are rejected.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: Permissions, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/query/groups
 */
export interface GroupsRequestBuilder extends BaseRequestBuilder<GroupsRequestBuilder> {
	/**
	 * Query Groups
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<QueryGroupPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: QueryRequest, requestConfiguration?: RequestConfiguration<GroupsRequestBuilderPostQueryParameters> | undefined): Promise<QueryGroupPage | undefined>;
	/**
	 * Query Groups
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: QueryRequest, requestConfiguration?: RequestConfiguration<GroupsRequestBuilderPostQueryParameters> | undefined): RequestInformation;
}
/**
 * Query Groups
 */
export interface GroupsRequestBuilderPostQueryParameters {
	/**
	 * Continuation from @odata.nextLink; resend the same query body.
	 */
	skiptoken?: string;
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/query
 */
export interface QueryRequestBuilder extends BaseRequestBuilder<QueryRequestBuilder> {
	/**
	 * The groups property
	 */
	get groups(): GroupsRequestBuilder;
	/**
	 *  Queries include collection items and folders; folder metadata is live on both surfaces. Indexed filters, sorting, selection, totals and pagination apply to both.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<QueryResult>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: QueryRequest, requestConfiguration?: RequestConfiguration<QueryRequestBuilderPostQueryParameters> | undefined): Promise<QueryResult | undefined>;
	/**
	 *  Queries include collection items and folders; folder metadata is live on both surfaces. Indexed filters, sorting, selection, totals and pagination apply to both.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: QueryRequest, requestConfiguration?: RequestConfiguration<QueryRequestBuilderPostQueryParameters> | undefined): RequestInformation;
}
/**
 *  Queries include collection items and folders; folder metadata is live on both surfaces. Indexed filters, sorting, selection, totals and pagination apply to both.
 */
export interface QueryRequestBuilderPostQueryParameters {
	/**
	 * Continuation from @odata.nextLink; resend the same query body.
	 */
	skiptoken?: string;
	top?: number;
}
interface RestoreRequestBuilder$2 extends BaseRequestBuilder<RestoreRequestBuilder$2> {
	/**
	 * Requires list.write and write access to the retained scope and destination. Returns to the active original folder, otherwise the collection root. Restores head/published content, history and indexes atomically. recursive=true restores folder descendants from the same deletion only. Name or business-key conflicts roll back the entire restore. Retained relationships become visible when both endpoints are active and readable. No If-Match is required.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(requestConfiguration?: RequestConfiguration<RestoreRequestBuilderPostQueryParameters> | undefined): Promise<Resource | undefined>;
	/**
	 * Requires list.write and write access to the retained scope and destination. Returns to the active original folder, otherwise the collection root. Restores head/published content, history and indexes atomically. recursive=true restores folder descendants from the same deletion only. Name or business-key conflicts roll back the entire restore. Retained relationships become visible when both endpoints are active and readable. No If-Match is required.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<RestoreRequestBuilderPostQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires list.write and write access to the retained scope and destination. Returns to the active original folder, otherwise the collection root. Restores head/published content, history and indexes atomically. recursive=true restores folder descendants from the same deletion only. Name or business-key conflicts roll back the entire restore. Retained relationships become visible when both endpoints are active and readable. No If-Match is required.
 */
export interface RestoreRequestBuilderPostQueryParameters {
	recursive?: boolean;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/recycle-bin/{itemID}
 */
export interface WithItemItemRequestBuilder extends BaseRequestBuilder<WithItemItemRequestBuilder> {
	/**
	 * The restore property
	 */
	get restore(): RestoreRequestBuilder$2;
	/**
	 * Requires list.write and manage access to every purged entry. recursive=true explicitly purges deleted folder descendants. Deletes retained revisions, publications, blob references and grants; audit/run records retain stable IDs. Object deletion is queued durably and retried after storage failures or restart. Only deleted entries in live collections can be purged. No If-Match is required.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<WithItemItemRequestBuilderDeleteQueryParameters> | undefined): Promise<void>;
	/**
	 * Requires list.write and manage access to every purged entry. recursive=true explicitly purges deleted folder descendants. Deletes retained revisions, publications, blob references and grants; audit/run records retain stable IDs. Object deletion is queued durably and retried after storage failures or restart. Only deleted entries in live collections can be purged. No If-Match is required.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<WithItemItemRequestBuilderDeleteQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires list.write and manage access to every purged entry. recursive=true explicitly purges deleted folder descendants. Deletes retained revisions, publications, blob references and grants; audit/run records retain stable IDs. Object deletion is queued durably and retried after storage failures or restart. Only deleted entries in live collections can be purged. No If-Match is required.
 */
export interface WithItemItemRequestBuilderDeleteQueryParameters {
	recursive?: boolean;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/recycle-bin
 */
export interface RecycleBinRequestBuilder extends BaseRequestBuilder<RecycleBinRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.resources.item.recycleBin.item collection
	 * @param itemID Unique identifier of the item
	 * @returns {WithItemItemRequestBuilder}
	 */
	byItemID(itemID: Guid): WithItemItemRequestBuilder;
	/**
	 * Requires a live readable collection and list.read; entries are filtered by list.write and their retained ACL scopes. Deleted content is excluded from ordinary reads and queries.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RecycleBinPage>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<RecycleBinRequestBuilderGetQueryParameters> | undefined): Promise<RecycleBinPage | undefined>;
	/**
	 * Requires a live readable collection and list.read; entries are filtered by list.write and their retained ACL scopes. Deleted content is excluded from ordinary reads and queries.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<RecycleBinRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires a live readable collection and list.read; entries are filtered by list.write and their retained ACL scopes. Deleted content is excluded from ordinary reads and queries.
 */
export interface RecycleBinRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
export interface SchemasGetResponse extends AdditionalDataHolder, Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: SchemaRevision[] | null;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/schemas
 */
export interface SchemasRequestBuilder extends BaseRequestBuilder<SchemasRequestBuilder> {
	/**
	 * Read immutable schema history (manage required)
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SchemasGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<SchemasRequestBuilderGetQueryParameters> | undefined): Promise<SchemasGetResponse | undefined>;
	/**
	 * Read immutable schema history (manage required)
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<SchemasRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Read immutable schema history (manage required)
 */
export interface SchemasRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/templates/{templateID}/apply
 */
export interface ApplyRequestBuilder extends BaseRequestBuilder<ApplyRequestBuilder> {
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply.
	 * @param body Apply compatible shared catalog field definitions and merge rules by key into the selected content type (default when omitted), recording one frozen collection schema. Other type memberships are retained.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 428 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: ApplyTemplateInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply.
	 * @param body Apply compatible shared catalog field definitions and merge rules by key into the selected content type (default when omitted), recording one frozen collection schema. Other type memberships are retained.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ApplyTemplateInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/templates/{templateID}
 */
export interface WithTemplateItemRequestBuilder extends BaseRequestBuilder<WithTemplateItemRequestBuilder> {
	/**
	 * The apply property
	 */
	get apply(): ApplyRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/templates
 */
export interface TemplatesRequestBuilder extends BaseRequestBuilder<TemplatesRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.resources.item.templates.item collection
	 * @param templateID Unique identifier of the item
	 * @returns {WithTemplateItemRequestBuilder}
	 */
	byTemplateID(templateID: string): WithTemplateItemRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/resources/{id}/views
 */
export interface ViewsRequestBuilder extends BaseRequestBuilder<ViewsRequestBuilder> {
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ListViewPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<ViewsRequestBuilderGetQueryParameters> | undefined): Promise<ListViewPage | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Collection-managed saved view. Columns use field keys and $id/$name/$keywords; empty columns default to $name. PUT replaces the definition. Layout is a client hint.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ListView>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: ViewInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ListView | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ViewsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Collection-managed saved view. Columns use field keys and $id/$name/$keywords; empty columns default to $name. PUT replaces the definition. Layout is a client hint.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ViewInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface ViewsRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
type GetSurfaceQueryParameterType$3 = (typeof GetSurfaceQueryParameterTypeObject$3)[keyof typeof GetSurfaceQueryParameterTypeObject$3];
/**
 * Builds and executes requests for operations under /v1/resources/{id}
 */
export interface ResourcesItemRequestBuilder extends BaseRequestBuilder<ResourcesItemRequestBuilder> {
	/**
	 * The bulk property
	 */
	get bulk(): BulkRequestBuilder;
	/**
	 * The bulkUpdate property
	 */
	get bulkUpdate(): BulkUpdateRequestBuilder;
	/**
	 * The calendar property
	 */
	get calendar(): CalendarRequestBuilder;
	/**
	 * The children property
	 */
	get children(): ChildrenRequestBuilder;
	/**
	 * The contentTypes property
	 */
	get contentTypes(): ContentTypesRequestBuilder$1;
	/**
	 * The delta property
	 */
	get delta(): DeltaRequestBuilder;
	/**
	 * The documents property
	 */
	get documents(): DocumentsRequestBuilder;
	/**
	 * The fields property
	 */
	get fields(): FieldsRequestBuilder$1;
	/**
	 * The integrations property
	 */
	get integrations(): IntegrationsRequestBuilder;
	/**
	 * The moveSelection property
	 */
	get moveSelection(): MoveSelectionRequestBuilder;
	/**
	 * The operations property
	 */
	get operations(): OperationsRequestBuilder$1;
	/**
	 * The permissions property
	 */
	get permissions(): PermissionsRequestBuilder;
	/**
	 * The query property
	 */
	get query(): QueryRequestBuilder;
	/**
	 * The recycleBin property
	 */
	get recycleBin(): RecycleBinRequestBuilder;
	/**
	 * The schemas property
	 */
	get schemas(): SchemasRequestBuilder;
	/**
	 * The templates property
	 */
	get templates(): TemplatesRequestBuilder;
	/**
	 * The views property
	 */
	get views(): ViewsRequestBuilder;
	/**
	 * Deletes an item or empty folder with write access and the current If-Match. For a folder, recursive=true deletes the folder and all live descendants atomically, requiring write access to every descendant. Without recursive=true, any live child, including a hidden item or nested folder, returns 409 with code folder_not_empty. Deleted children do not prevent ordinary deletion. Permission failures and stale versions leave the entire tree unchanged. Workspace and collection deletion require manage access and remove the entire live resource tree independently of recursive; collection managers may delete the entire collection regardless of descendant ACLs. Tombstones, revisions and audit history are retained. Read projections and live relationships are removed, and each deleted item emits item.deleted. Workflows bound to a deleted workspace or collection are disabled and deleted.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<ResourcesItemRequestBuilderDeleteQueryParameters> | undefined): Promise<void>;
	/**
	 * Read resource metadata
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ResourceAccess>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ResourcesItemRequestBuilderGetQueryParameters> | undefined): Promise<ResourceAccess | undefined>;
	/**
	 * Workspace and collection metadata changes require manage access. Items and folders require write access. If-Match is required.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ResourceAccess>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	patch(body: UpdateResource, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ResourceAccess | undefined>;
	/**
	 * Deletes an item or empty folder with write access and the current If-Match. For a folder, recursive=true deletes the folder and all live descendants atomically, requiring write access to every descendant. Without recursive=true, any live child, including a hidden item or nested folder, returns 409 with code folder_not_empty. Deleted children do not prevent ordinary deletion. Permission failures and stale versions leave the entire tree unchanged. Workspace and collection deletion require manage access and remove the entire live resource tree independently of recursive; collection managers may delete the entire collection regardless of descendant ACLs. Tombstones, revisions and audit history are retained. Read projections and live relationships are removed, and each deleted item emits item.deleted. Workflows bound to a deleted workspace or collection are disabled and deleted.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<ResourcesItemRequestBuilderDeleteQueryParameters> | undefined): RequestInformation;
	/**
	 * Read resource metadata
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ResourcesItemRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Workspace and collection metadata changes require manage access. Items and folders require write access. If-Match is required.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPatchRequestInformation(body: UpdateResource, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Deletes an item or empty folder with write access and the current If-Match. For a folder, recursive=true deletes the folder and all live descendants atomically, requiring write access to every descendant. Without recursive=true, any live child, including a hidden item or nested folder, returns 409 with code folder_not_empty. Deleted children do not prevent ordinary deletion. Permission failures and stale versions leave the entire tree unchanged. Workspace and collection deletion require manage access and remove the entire live resource tree independently of recursive; collection managers may delete the entire collection regardless of descendant ACLs. Tombstones, revisions and audit history are retained. Read projections and live relationships are removed, and each deleted item emits item.deleted. Workflows bound to a deleted workspace or collection are disabled and deleted.
 */
export interface ResourcesItemRequestBuilderDeleteQueryParameters {
	/**
	 * For folders, true explicitly deletes the folder and all its contents; false requires an empty folder. Write access is required on every descendant. Must occur at most once and be exactly true or false.
	 */
	recursive?: boolean;
}
/**
 * Read resource metadata
 */
export interface ResourcesItemRequestBuilderGetQueryParameters {
	/**
	 * Auto chooses head for draft readers and published for ordinary readers. Head requires draft access.
	 */
	surface?: GetSurfaceQueryParameterType$3;
}
declare const GetSurfaceQueryParameterTypeObject$3: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
type GetFilter_opQueryParameterType$1 = (typeof GetFilter_opQueryParameterTypeObject$1)[keyof typeof GetFilter_opQueryParameterTypeObject$1];
type GetKindQueryParameterType$2 = (typeof GetKindQueryParameterTypeObject$2)[keyof typeof GetKindQueryParameterTypeObject$2];
type GetSurfaceQueryParameterType$4 = (typeof GetSurfaceQueryParameterTypeObject$4)[keyof typeof GetSurfaceQueryParameterTypeObject$4];
export interface ResourcesGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The search text matched more than 10,000 entries; the page covers only the first 10,000 matches. Narrow the search.
	 */
	searchCapped?: boolean | null;
	/**
	 * The value property
	 */
	value?: Resource[] | null;
}
/**
 * Builds and executes requests for operations under /v1/resources
 */
export interface ResourcesRequestBuilder extends BaseRequestBuilder<ResourcesRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.resources.item collection
	 * @param id Unique identifier of the item
	 * @returns {ResourcesItemRequestBuilder}
	 */
	byId(id: Guid): ResourcesItemRequestBuilder;
	/**
	 * Browse/search a workspace or parent scope
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ResourcesGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ResourcesRequestBuilderGetQueryParameters> | undefined): Promise<ResourcesGetResponse | undefined>;
	/**
	 * Browse/search a workspace or parent scope
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ResourcesRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Browse/search a workspace or parent scope
 */
export interface ResourcesRequestBuilderGetQueryParameters {
	archive?: ArchiveSelection;
	/**
	 * Opt-in indexed field key; requires list/library/folder parent scope.
	 */
	filterField?: string;
	/**
	 * Typed comparison. Boolean supports eq only.
	 */
	filterOp?: GetFilter_opQueryParameterType$1;
	/**
	 * Typed value, parsed without floating-point conversion for integer and decimal fields.
	 */
	filterValue?: string;
	/**
	 * Shared keyword term ID or label; includes intrinsic keywords and keywords fields.
	 */
	keyword?: string;
	/**
	 * Only resources of this kind, e.g. kind=folder for the folders of a large library.
	 */
	kind?: GetKindQueryParameterType$2;
	parentId?: string;
	q?: string;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Auto chooses head for draft readers and published for ordinary readers. Head requires draft access.
	 */
	surface?: GetSurfaceQueryParameterType$4;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
	workspaceId?: string;
}
declare const GetFilter_opQueryParameterTypeObject$1: {
	readonly Eq: "eq";
	readonly Gt: "gt";
	readonly Gte: "gte";
	readonly Lt: "lt";
	readonly Lte: "lte";
};
declare const GetKindQueryParameterTypeObject$2: {
	readonly Workspace: "workspace";
	readonly List: "list";
	readonly Library: "library";
	readonly Folder: "folder";
	readonly Item: "item";
};
declare const GetSurfaceQueryParameterTypeObject$4: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
export interface ContentTypes extends AdditionalDataHolder, Parsable {
	/**
	 * The id property
	 */
	id?: Guid | null;
	/**
	 * The key property
	 */
	key?: string | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
interface ContentTypesRequestBuilder$2 extends BaseRequestBuilder<ContentTypesRequestBuilder$2> {
	/**
	 * Requires list.read. Optional workspace and collection scopes narrow the catalog; organization catalog permissions are not required.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ContentTypes[]>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ContentTypesRequestBuilderGetQueryParameters> | undefined): Promise<ContentTypes[] | undefined>;
	/**
	 * Requires list.read. Optional workspace and collection scopes narrow the catalog; organization catalog permissions are not required.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ContentTypesRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires list.read. Optional workspace and collection scopes narrow the catalog; organization catalog permissions are not required.
 */
export interface ContentTypesRequestBuilderGetQueryParameters {
	collectionId?: Guid;
	workspaceId?: Guid;
}
interface FieldsGetResponse$1 extends Parsable {
	/**
	 * The value property
	 */
	value?: SearchField[] | null;
}
interface FieldsRequestBuilder$2 extends BaseRequestBuilder<FieldsRequestBuilder$2> {
	/**
	 * The fields of the lists and libraries the caller may read, one per key, type and scale. Notes are text, not filters.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<FieldsGetResponse>}
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 */
	get(requestConfiguration?: RequestConfiguration<FieldsRequestBuilderGetQueryParameters> | undefined): Promise<FieldsGetResponse$1 | undefined>;
	/**
	 * The fields of the lists and libraries the caller may read, one per key, type and scale. Notes are text, not filters.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<FieldsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * The fields of the lists and libraries the caller may read, one per key, type and scale. Notes are text, not filters.
 */
export interface FieldsRequestBuilderGetQueryParameters {
	collectionId?: string;
	workspaceId?: string;
}
interface ChunksRequestBuilder$1 extends BaseRequestBuilder<ChunksRequestBuilder$1> {
	/**
	 * The staged or published chunks of a generation the caller started, by part and ordinal, a page at a time.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<GenerationChunkPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	get(requestConfiguration?: RequestConfiguration<ChunksRequestBuilderGetQueryParameters> | undefined): Promise<GenerationChunkPage | undefined>;
	/**
	 * The staged or published chunks of a generation the caller started, by part and ordinal, a page at a time.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ChunksRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * The staged or published chunks of a generation the caller started, by part and ordinal, a page at a time.
 */
export interface ChunksRequestBuilderGetQueryParameters {
	/**
	 * 1 to 100, 20 when omitted.
	 */
	limit?: number;
	offset?: number;
}
/**
 * Builds and executes requests for operations under /v1/search/generations/{id}/embeddings
 */
export interface EmbeddingsRequestBuilder extends BaseRequestBuilder<EmbeddingsRequestBuilder> {
	/**
	 * Gives the chunks of a generation the caller started their embedding vectors, reusing vectors of equal chunk inputs. Without an embedding model, or when the model fails, the result says why and the embedding sweep embeds the chunks once they are published.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Gives the chunks of a generation the caller started their embedding vectors, reusing vectors of equal chunk inputs. Without an embedding model, or when the model fails, the result says why and the embedding sweep embeds the chunks once they are published.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/search/generations/{id}/enrichments
 */
export interface EnrichmentsRequestBuilder extends BaseRequestBuilder<EnrichmentsRequestBuilder> {
	/**
	 * Sets the enrichment of chunks of a generation the caller started; chunks it does not name keep theirs. The enrichment is searched with its chunk and embedded with it, so embed after enriching.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	put(body: GenerationEnrichments, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * Sets the enrichment of chunks of a generation the caller started; chunks it does not name keep theirs. The enrichment is searched with its chunk and embedded with it, so embed after enriching.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: GenerationEnrichments, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/search/generations/{id}/publish
 */
export interface PublishRequestBuilder extends BaseRequestBuilder<PublishRequestBuilder> {
	/**
	 * Replaces the searchable content of the generation's file version with the generation, unless the item is gone or no longer has that file: a slow workflow never replaces a newer file's content.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Replaces the searchable content of the generation's file version with the generation, unless the item is gone or no longer has that file: a slow workflow never replaces a newer file's content.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/search/generations/{id}
 */
export interface GenerationsItemRequestBuilder extends BaseRequestBuilder<GenerationsItemRequestBuilder> {
	/**
	 * The chunks property
	 */
	get chunks(): ChunksRequestBuilder$1;
	/**
	 * The embeddings property
	 */
	get embeddings(): EmbeddingsRequestBuilder;
	/**
	 * The enrichments property
	 */
	get enrichments(): EnrichmentsRequestBuilder;
	/**
	 * The publish property
	 */
	get publish(): PublishRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/search/generations
 */
export interface GenerationsRequestBuilder extends BaseRequestBuilder<GenerationsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.search.generations.item collection
	 * @param id The generation: the ID of the chunk operation that started it.
	 * @returns {GenerationsItemRequestBuilder}
	 */
	byId(id: string): GenerationsItemRequestBuilder;
}
export interface ReindexPostRequestBody extends Parsable {
	/**
	 * Also index file content again, through file.updated events with the reindex source.
	 */
	content?: boolean | null;
	/**
	 * With content: every current file, not only those without content.
	 */
	force?: boolean | null;
}
/**
 * Builds and executes requests for operations under /v1/search/reindex
 */
export interface ReindexRequestBuilder extends BaseRequestBuilder<ReindexRequestBuilder> {
	/**
	 * Rewrites the documents of every live item and folder in batches and removes documents of entries that no longer exist. With content, the workflow filling search.content reads, chunks and publishes again the current file of items whose file has no content (all of them with force); text read before is reused. Search keeps answering meanwhile. Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 429 status code
	 * @throws {Problem} error when the service returns a 503 status code
	 */
	post(body: ReindexPostRequestBody, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Rewrites the documents of every live item and folder in batches and removes documents of entries that no longer exist. With content, the workflow filling search.content reads, chunks and publishes again the current file of items whose file has no content (all of them with force); text read before is reused. Search keeps answering meanwhile. Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ReindexPostRequestBody, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/search/status
 */
export interface StatusRequestBuilder extends BaseRequestBuilder<StatusRequestBuilder> {
	/**
	 * Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SearchStatus>}
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 503 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SearchStatus | undefined>;
	/**
	 * Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export type GetModeQueryParameterType = (typeof GetModeQueryParameterTypeObject)[keyof typeof GetModeQueryParameterTypeObject];
type GetSurfaceQueryParameterType$5 = (typeof GetSurfaceQueryParameterTypeObject$5)[keyof typeof GetSurfaceQueryParameterTypeObject$5];
interface SearchRequestBuilder$1 extends BaseRequestBuilder<SearchRequestBuilder$1> {
	/**
	 * The contentTypes property
	 */
	get contentTypes(): ContentTypesRequestBuilder$2;
	/**
	 * The fields property
	 */
	get fields(): FieldsRequestBuilder$2;
	/**
	 * The generations property
	 */
	get generations(): GenerationsRequestBuilder;
	/**
	 * The reindex property
	 */
	get reindex(): ReindexRequestBuilder;
	/**
	 * The status property
	 */
	get status(): StatusRequestBuilder;
	/**
	 * Keyword search over the search index, trimmed to what the caller reads now and on the surface they read; every hit is rechecked against current permissions. File text participates in matching and snippets only with document.read; otherwise metadata is searched. Facets are omitted if any matching source fails current authorization. Indexing is asynchronous (the search.index workflow). See docs/search.md. Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SearchPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 503 status code
	 */
	get(requestConfiguration?: RequestConfiguration<SearchRequestBuilderGetQueryParameters> | undefined): Promise<SearchPage | undefined>;
	/**
	 * Keyword search over the search index, trimmed to what the caller reads now and on the surface they read; every hit is rechecked against current permissions. File text participates in matching and snippets only with document.read; otherwise metadata is searched. Facets are omitted if any matching source fails current authorization. Indexing is asynchronous (the search.index workflow). See docs/search.md. The JSON body takes the GET parameters (kind as an array) and a filter on search fields; $top and $skiptoken stay in the URL, and a next page repeats the body. Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SearchPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 503 status code
	 */
	post(body: SearchRequest, requestConfiguration?: RequestConfiguration<SearchRequestBuilderPostQueryParameters> | undefined): Promise<SearchPage | undefined>;
	/**
	 * Keyword search over the search index, trimmed to what the caller reads now and on the surface they read; every hit is rechecked against current permissions. File text participates in matching and snippets only with document.read; otherwise metadata is searched. Facets are omitted if any matching source fails current authorization. Indexing is asynchronous (the search.index workflow). See docs/search.md. Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<SearchRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Keyword search over the search index, trimmed to what the caller reads now and on the surface they read; every hit is rechecked against current permissions. File text participates in matching and snippets only with document.read; otherwise metadata is searched. Facets are omitted if any matching source fails current authorization. Indexing is asynchronous (the search.index workflow). See docs/search.md. The JSON body takes the GET parameters (kind as an array) and a filter on search fields; $top and $skiptoken stay in the URL, and a next page repeats the body. Served when the deployment runs search (SEARCH_STORE is not none).
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SearchRequest, requestConfiguration?: RequestConfiguration<SearchRequestBuilderPostQueryParameters> | undefined): RequestInformation;
}
/**
 * Keyword search over the search index, trimmed to what the caller reads now and on the surface they read; every hit is rechecked against current permissions. File text participates in matching and snippets only with document.read; otherwise metadata is searched. Facets are omitted if any matching source fails current authorization. Indexing is asynchronous (the search.index workflow). See docs/search.md. Served when the deployment runs search (SEARCH_STORE is not none).
 */
export interface SearchRequestBuilderGetQueryParameters {
	archive?: ArchiveSelection;
	archivedBy?: string;
	archivedFrom?: Date;
	archivedTo?: Date;
	/**
	 * Only this list or library.
	 */
	collectionId?: string;
	/**
	 * Only this content type.
	 */
	contentTypeId?: string;
	/**
	 * Only entries created by this subject.
	 */
	createdBy?: string;
	/**
	 * false leaves out facet counts.
	 */
	facets?: boolean;
	/**
	 * Shared keyword term ID or label; includes intrinsic keywords and keywords fields.
	 */
	keyword?: string;
	/**
	 * item, folder, or both comma-separated.
	 */
	kind?: string;
	/**
	 * keyword matches words; semantic finds file content close in meaning (an embedding model is required); hybrid fuses both by reciprocal rank. Default hybrid when an embedding model is configured and q is given, else keyword.
	 */
	mode?: GetModeQueryParameterType;
	/**
	 * Only entries last modified by this subject.
	 */
	modifiedBy?: string;
	/**
	 * Modified at or after (RFC 3339).
	 */
	modifiedFrom?: Date;
	/**
	 * Modified at or before (RFC 3339).
	 */
	modifiedTo?: Date;
	/**
	 * Words (all must match), "phrases", a OR b, -word or NOT word, prefix*. Required unless a filter is given.
	 */
	q?: string;
	/**
	 * Opaque continuation from @odata.nextLink, bound to the caller and the query.
	 */
	skiptoken?: string;
	/**
	 * Auto chooses head where the caller reads drafts and published elsewhere.
	 */
	surface?: GetSurfaceQueryParameterType$5;
	/**
	 * Items holding this term, a descendant, or a term merged into it.
	 */
	termId?: string;
	/**
	 * Page size.
	 */
	top?: number;
	/**
	 * Only this workspace.
	 */
	workspaceId?: string;
}
/**
 * Keyword search over the search index, trimmed to what the caller reads now and on the surface they read; every hit is rechecked against current permissions. File text participates in matching and snippets only with document.read; otherwise metadata is searched. Facets are omitted if any matching source fails current authorization. Indexing is asynchronous (the search.index workflow). See docs/search.md. The JSON body takes the GET parameters (kind as an array) and a filter on search fields; $top and $skiptoken stay in the URL, and a next page repeats the body. Served when the deployment runs search (SEARCH_STORE is not none).
 */
export interface SearchRequestBuilderPostQueryParameters {
	/**
	 * Opaque continuation from @odata.nextLink, bound to the caller and the query.
	 */
	skiptoken?: string;
	/**
	 * Page size.
	 */
	top?: number;
}
declare const GetModeQueryParameterTypeObject: {
	readonly Keyword: "keyword";
	readonly Semantic: "semantic";
	readonly Hybrid: "hybrid";
};
declare const GetSurfaceQueryParameterTypeObject$5: {
	readonly Auto: "auto";
	readonly Head: "head";
	readonly Published: "published";
};
interface WithItemItemRequestBuilder$1 extends BaseRequestBuilder<WithItemItemRequestBuilder$1> {
	/**
	 * Requires item If-Match and current folder_version. Clears matching equality values/keywords and selected terms including descendants; ordinary validation and publication rules apply. Rolls back if the result still matches the definition. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Only definitions with mode classification permit this action; saved queries reject it.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(body: SmartFolderUnclassify, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 * Requires item If-Match and current folder_version. Clears matching equality values/keywords and selected terms including descendants; ordinary validation and publication rules apply. Rolls back if the result still matches the definition. Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Only definitions with mode classification permit this action; saved queries reject it.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(body: SmartFolderUnclassify, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface ItemsRequestBuilder$1 extends BaseRequestBuilder<ItemsRequestBuilder$1> {
	/**
	 * Gets an item from the PaperGo.v1.smartFolders.item.items.item collection
	 * @param itemID Unique identifier of the item
	 * @returns {WithItemItemRequestBuilder}
	 */
	byItemID(itemID: Guid): WithItemItemRequestBuilder$1;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Only definitions with mode classification permit this action; saved queries reject it.
	 * @param body Classify existing item or create one through ordinary item validation/revisions/publication. Current folder_version is required. Inferred AND equalities, selected terms and writable path fields are applied; non-inferable predicates must already fit. The write rolls back unless the head matches the folder/path. Existing items retain location; new parent_id must belong to collection.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: SmartFolderDrop, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply. Only definitions with mode classification permit this action; saved queries reject it.
	 * @param body Classify existing item or create one through ordinary item validation/revisions/publication. Current folder_version is required. Inferred AND equalities, selected terms and writable path fields are applied; non-inferable predicates must already fit. The write rolls back unless the head matches the folder/path. Existing items retain location; new parent_id must belong to collection.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SmartFolderDrop, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface GroupsRequestBuilder$1 extends BaseRequestBuilder<GroupsRequestBuilder$1> {
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink. An empty path returns all matching items even when group_by is configured.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolderGroupsResult>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: SmartFolderQueryRequest, requestConfiguration?: RequestConfiguration<GroupsRequestBuilderPostQueryParameters$1> | undefined): Promise<SmartFolderGroupsResult | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink. An empty path returns all matching items even when group_by is configured.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SmartFolderQueryRequest, requestConfiguration?: RequestConfiguration<GroupsRequestBuilderPostQueryParameters$1> | undefined): RequestInformation;
}
interface GroupsRequestBuilderPostQueryParameters$1 {
	/**
	 * Continuation from @odata.nextLink; resend the same query body.
	 */
	skiptoken?: string;
	top?: number;
}
interface QueryRequestBuilder$1 extends BaseRequestBuilder<QueryRequestBuilder$1> {
	/**
	 * The groups property
	 */
	get groups(): GroupsRequestBuilder$1;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink. An empty path returns all matching items even when group_by is configured.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolderResult>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: SmartFolderQueryRequest, requestConfiguration?: RequestConfiguration<QueryRequestBuilderPostQueryParameters$1> | undefined): Promise<SmartFolderResult | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink. An empty path returns all matching items even when group_by is configured.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SmartFolderQueryRequest, requestConfiguration?: RequestConfiguration<QueryRequestBuilderPostQueryParameters$1> | undefined): RequestInformation;
}
interface QueryRequestBuilderPostQueryParameters$1 {
	/**
	 * Continuation from @odata.nextLink; resend the same query body.
	 */
	skiptoken?: string;
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/smart-folders/{id}
 */
export interface SmartFoldersItemRequestBuilder extends BaseRequestBuilder<SmartFoldersItemRequestBuilder> {
	/**
	 * The items property
	 */
	get items(): ItemsRequestBuilder$1;
	/**
	 * The query property
	 */
	get query(): QueryRequestBuilder$1;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolder>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SmartFolder | undefined>;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param body Personal folders are owner-only and may span all readable workspaces. Shared folders require workspace_id and workspace manage permission. Ownership and workspace are immutable on PUT; PUT replaces the definition. At most 100 shared folders per workspace. Shared mutations advance the workspace version; personal mutations do not.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolder>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	put(body: SmartFolderInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SmartFolder | undefined>;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param body Personal folders are owner-only and may span all readable workspaces. Shared folders require workspace_id and workspace manage permission. Ownership and workspace are immutable on PUT; PUT replaces the definition. At most 100 shared folders per workspace. Shared mutations advance the workspace version; personal mutations do not.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: SmartFolderInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface PreviewRequestBuilder$1 extends BaseRequestBuilder<PreviewRequestBuilder$1> {
	/**
	 * Read-only evaluation of the caller’s readable collections and visible item revisions. Returns up to five items and a count capped at 10,000; no continuation. No definition, item, version or audit event is saved. Requires list.read.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolderResult>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: SmartFolderPreview, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SmartFolderResult | undefined>;
	/**
	 * Read-only evaluation of the caller’s readable collections and visible item revisions. Returns up to five items and a count capped at 10,000; no continuation. No definition, item, version or audit event is saved. Requires list.read.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SmartFolderPreview, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/smart-folders
 */
export interface SmartFoldersRequestBuilder extends BaseRequestBuilder<SmartFoldersRequestBuilder> {
	/**
	 * The preview property
	 */
	get preview(): PreviewRequestBuilder$1;
	/**
	 * Gets an item from the PaperGo.v1.smartFolders.item collection
	 * @param id Unique identifier of the item
	 * @returns {SmartFoldersItemRequestBuilder}
	 */
	byId(id: Guid): SmartFoldersItemRequestBuilder;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolderPage>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<SmartFoldersRequestBuilderGetQueryParameters> | undefined): Promise<SmartFolderPage | undefined>;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param body Personal folders are owner-only and may span all readable workspaces. Shared folders require workspace_id and workspace manage permission. Ownership and workspace are immutable on PUT; PUT replaces the definition. At most 100 shared folders per workspace. Shared mutations advance the workspace version; personal mutations do not.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolder>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: SmartFolderInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SmartFolder | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<SmartFoldersRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 *  Required module scopes: list.write. Resource ACLs and any additional use-case checks also apply.
	 * @param body Personal folders are owner-only and may span all readable workspaces. Shared folders require workspace_id and workspace manage permission. Ownership and workspace are immutable on PUT; PUT replaces the definition. At most 100 shared folders per workspace. Shared mutations advance the workspace version; personal mutations do not.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SmartFolderInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface SmartFoldersRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
	workspaceId?: Guid;
}
interface ContentRequestBuilder$2 extends BaseRequestBuilder<ContentRequestBuilder$2> {
	/**
	 * The staged file's bytes, inline, to readers of its item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ArrayBuffer>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ArrayBuffer | undefined>;
	/**
	 * The staged file's bytes, inline, to readers of its item.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/staged-files/{id}/discard
 */
export interface DiscardRequestBuilder extends BaseRequestBuilder<DiscardRequestBuilder> {
	/**
	 * Discards a staged file; its item keeps its current file and the artifact stays for reuse until it expires. A file a workflow run staged answers only that run, one staged without a run only callers without one (422 otherwise).
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * Discards a staged file; its item keeps its current file and the artifact stays for reuse until it expires. A file a workflow run staged answers only that run, one staged without a run only callers without one (422 otherwise).
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface PromoteRequestBuilder$1 extends BaseRequestBuilder<PromoteRequestBuilder$1> {
	/**
	 * Makes a staged file its item's current file, as a new file version. A file a workflow run staged answers only that run, one staged without a run only callers without one (422 otherwise), each with write access to the item. Fails with 409 when a file it was made from is no longer current. With recycle_sources, moves the other source items to the recycle bin in the same transaction.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<PromotedFile>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	post(body: PromoteStagedFile, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<PromotedFile | undefined>;
	/**
	 * Makes a staged file its item's current file, as a new file version. A file a workflow run staged answers only that run, one staged without a run only callers without one (422 otherwise), each with write access to the item. Fails with 409 when a file it was made from is no longer current. With recycle_sources, moves the other source items to the recycle bin in the same transaction.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: PromoteStagedFile, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/staged-files/{id}
 */
export interface StagedFilesItemRequestBuilder extends BaseRequestBuilder<StagedFilesItemRequestBuilder> {
	/**
	 * The content property
	 */
	get content(): ContentRequestBuilder$2;
	/**
	 * The discard property
	 */
	get discard(): DiscardRequestBuilder;
	/**
	 * The promote property
	 */
	get promote(): PromoteRequestBuilder$1;
	/**
	 * Get Staged File
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<StagedFile>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<StagedFile | undefined>;
	/**
	 * Get Staged File
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/staged-files
 */
export interface StagedFilesRequestBuilder extends BaseRequestBuilder<StagedFilesRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.stagedFiles.item collection
	 * @param id Unique identifier of the item
	 * @returns {StagedFilesItemRequestBuilder}
	 */
	byId(id: string): StagedFilesItemRequestBuilder;
}
interface CancelRequestBuilder$1 extends BaseRequestBuilder<CancelRequestBuilder$1> {
	/**
	 * Cancels an open task; its creator or a workspace owner may. A run waiting for a workflow's task gets the cancellation as the outcome of its wait.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Task>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Task | undefined>;
	/**
	 * Cancels an open task; its creator or a workspace owner may. A run waiting for a workflow's task gets the cancellation as the outcome of its wait.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/tasks/{id}/reassign
 */
export interface ReassignRequestBuilder extends BaseRequestBuilder<ReassignRequestBuilder> {
	/**
	 * Replaces an open task's assignees, entries as when a task is created, for example when an assignee was deactivated or lost access. A workspace owner may, and the creator of a person's task. Answers of entries that stay assigned count: a task with completion all completes once every entry left has answered, and a run waiting for it continues.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Task>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TaskReassign, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Task | undefined>;
	/**
	 * Replaces an open task's assignees, entries as when a task is created, for example when an assignee was deactivated or lost access. A workspace owner may, and the creator of a person's task. Answers of entries that stay assigned count: a task with completion all completes once every entry left has answered, and a run waiting for it continues.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TaskReassign, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/tasks/{id}/responses
 */
export interface ResponsesRequestBuilder extends BaseRequestBuilder<ResponsesRequestBuilder> {
	/**
	 * An assignee answers with one of the task's outcomes, its comment when the outcome asks for one, and the values of the task's form unless the outcome skips it. 409 when the task is no longer open, the caller already answered for every entry they hold, or the outcome acts on a review whose staged file was made from a file that is no longer current.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Task>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TaskResponseInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Task | undefined>;
	/**
	 * An assignee answers with one of the task's outcomes, its comment when the outcome asks for one, and the values of the task's form unless the outcome skips it. 409 when the task is no longer open, the caller already answered for every entry they hold, or the outcome acts on a review whose staged file was made from a file that is no longer current.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TaskResponseInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/tasks/{id}
 */
export interface TasksItemRequestBuilder extends BaseRequestBuilder<TasksItemRequestBuilder> {
	/**
	 * The cancel property
	 */
	get cancel(): CancelRequestBuilder$1;
	/**
	 * The reassign property
	 */
	get reassign(): ReassignRequestBuilder;
	/**
	 * The responses property
	 */
	get responses(): ResponsesRequestBuilder;
	/**
	 * A task with its responses, for its assignees, its creator and the workspace's owners.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Task>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Task | undefined>;
	/**
	 * A task with its responses, for its assignees, its creator and the workspace's owners.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface TasksRequestBuilder$1 extends BaseRequestBuilder<TasksRequestBuilder$1> {
	/**
	 * Gets an item from the PaperGo.v1.tasks.item collection
	 * @param id Unique identifier of the item
	 * @returns {TasksItemRequestBuilder}
	 */
	byId(id: string): TasksItemRequestBuilder;
}
interface ExportRequestBuilder$1 extends BaseRequestBuilder<ExportRequestBuilder$1> {
	/**
	 * Term groups, sets and nested terms by name; merged terms are left out. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TaxonomyPackage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TaxonomyPackage | undefined>;
	/**
	 * Term groups, sets and nested terms by name; merged terms are left out. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/taxonomy/import
 */
export interface ImportRequestBuilder extends BaseRequestBuilder<ImportRequestBuilder> {
	/**
	 * Additive: groups match by name (the system group by system), sets by key, terms by name under the same parent; missing ones are created. Requires taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TaxonomyImportResult>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TaxonomyPackage, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TaxonomyImportResult | undefined>;
	/**
	 * Additive: groups match by name (the system group by system), sets by key, terms by name under the same parent; missing ones are created. Requires taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TaxonomyPackage, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/taxonomy
 */
export interface TaxonomyRequestBuilder extends BaseRequestBuilder<TaxonomyRequestBuilder> {
	/**
	 * The export property
	 */
	get exportEscaped(): ExportRequestBuilder$1;
	/**
	 * The import property
	 */
	get importEscaped(): ImportRequestBuilder;
}
/**
 * Builds and executes requests for operations under /v1/templates/{id}
 */
export interface TemplatesItemRequestBuilder extends BaseRequestBuilder<TemplatesItemRequestBuilder> {
	/**
	 *  Required module scopes: contentType.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SchemaTemplate>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SchemaTemplate | undefined>;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SchemaTemplate>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 428 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	put(body: TemplateInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SchemaTemplate | undefined>;
	/**
	 *  Required module scopes: contentType.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: TemplateInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface TemplatesRequestBuilder$1 extends BaseRequestBuilder<TemplatesRequestBuilder$1> {
	/**
	 * Gets an item from the PaperGo.v1.templates.item collection
	 * @param id Unique identifier of the item
	 * @returns {TemplatesItemRequestBuilder}
	 */
	byId(id: string): TemplatesItemRequestBuilder;
}
interface ImportRequestBuilder$1 extends BaseRequestBuilder<ImportRequestBuilder$1> {
	/**
	 * Adds the sets and terms of a SharePoint term set CSV (at most 1 MiB) to the group. Existing sets (by name) and terms (by name under the same parent) are reused; new sets are closed. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body Binary request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermImportResult>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: ArrayBuffer | undefined, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TermImportResult | undefined>;
	/**
	 * Adds the sets and terms of a SharePoint term set CSV (at most 1 MiB) to the group. Existing sets (by name) and terms (by name under the same parent) are reused; new sets are closed. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body Binary request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ArrayBuffer | undefined, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/term-groups/{id}
 */
export interface TermGroupsItemRequestBuilder extends BaseRequestBuilder<TermGroupsItemRequestBuilder> {
	/**
	 * The import property
	 */
	get importEscaped(): ImportRequestBuilder$1;
	/**
	 * Deletes an empty group other than the system group. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermGroup>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TermGroup | undefined>;
	/**
	 * The system group cannot change. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermGroup>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	put(body: TermGroupInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TermGroup | undefined>;
	/**
	 * Deletes an empty group other than the system group. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * The system group cannot change. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: TermGroupInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/term-groups
 */
export interface TermGroupsRequestBuilder extends BaseRequestBuilder<TermGroupsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.termGroups.item collection
	 * @param id Unique identifier of the item
	 * @returns {TermGroupsItemRequestBuilder}
	 */
	byId(id: string): TermGroupsItemRequestBuilder;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermGroupPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TermGroupsRequestBuilderGetQueryParameters> | undefined): Promise<TermGroupPage | undefined>;
	/**
	 * Requires taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermGroup>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TermGroupInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TermGroup | undefined>;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TermGroupsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Requires taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TermGroupInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
 */
export interface TermGroupsRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/terms/{id}/merge
 */
export interface MergeRequestBuilder extends BaseRequestBuilder<MergeRequestBuilder> {
	/**
	 * Merges the term into another term of its set and returns the target: children move, the source's name and synonyms become synonyms, and the source keeps merged_into_id. Raises term.merged. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Term>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: MergeTerm, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Term | undefined>;
	/**
	 * Merges the term into another term of its set and returns the target: children move, the source's name and synonyms become synonyms, and the source keeps merged_into_id. Raises term.merged. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: MergeTerm, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface MoveRequestBuilder$1 extends BaseRequestBuilder<MoveRequestBuilder$1> {
	/**
	 * Moves the term and its subtree in its set. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Term>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: MoveTerm, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Term | undefined>;
	/**
	 * Moves the term and its subtree in its set. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: MoveTerm, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/terms/{id}
 */
export interface TermsItemRequestBuilder extends BaseRequestBuilder<TermsItemRequestBuilder> {
	/**
	 * The merge property
	 */
	get merge(): MergeRequestBuilder;
	/**
	 * The move property
	 */
	get move(): MoveRequestBuilder$1;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Term>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Term | undefined>;
	/**
	 * Requires manage. Merged terms are read-only. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy. Changes to the name, localized labels or other editable properties raise term.changed in every live workspace, with term_id, term_set_id, version, sorted changed_fields and previous/current term properties. Unchanged values raise no event; failed writes roll back every event. The event has no item or resource_id.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Term>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	put(body: TermInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Term | undefined>;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Requires manage. Merged terms are read-only. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy. Changes to the name, localized labels or other editable properties raise term.changed in every live workspace, with term_id, term_set_id, version, sorted changed_fields and previous/current term properties. Unchanged values raise no event; failed writes roll back every event. The event has no item or resource_id.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: TermInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/terms
 */
export interface TermsRequestBuilder extends BaseRequestBuilder<TermsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.terms.item collection
	 * @param id Unique identifier of the item
	 * @returns {TermsItemRequestBuilder}
	 */
	byId(id: string): TermsItemRequestBuilder;
	/**
	 * Readable terms in the order asked; unknown ids are left out. Merged terms name their replacement in merged_into_id. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermList>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TermsRequestBuilderGetQueryParameters> | undefined): Promise<TermList | undefined>;
	/**
	 * Readable terms in the order asked; unknown ids are left out. Merged terms name their replacement in merged_into_id. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TermsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Readable terms in the order asked; unknown ids are left out. Merged terms name their replacement in merged_into_id. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
 */
export interface TermsRequestBuilderGetQueryParameters {
	/**
	 * 1 to 200 comma-separated term ids.
	 */
	ids?: string;
}
interface TermsRequestBuilder$1 extends BaseRequestBuilder<TermsRequestBuilder$1> {
	/**
	 * Ordered by sort_order, then name. Merged terms are never listed. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TermsRequestBuilderGetQueryParameters$1> | undefined): Promise<TermPage | undefined>;
	/**
	 * Open sets require taxonomy.read; closed sets and deprecated or keyword-available terms require taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Term>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TermInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Term | undefined>;
	/**
	 * Ordered by sort_order, then name. Merged terms are never listed. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TermsRequestBuilderGetQueryParameters$1> | undefined): RequestInformation;
	/**
	 * Open sets require taxonomy.read; closed sets and deprecated or keyword-available terms require taxonomy.manage. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TermInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface TermsRequestBuilderGetQueryParameters$1 {
	/**
	 * Also list deprecated terms.
	 */
	includeDeprecated?: boolean;
	/**
	 * List this term's children; without it, the root terms. With q, search below this term.
	 */
	parentId?: string;
	/**
	 * Search names, labels and synonyms anywhere in the set.
	 */
	q?: string;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/term-sets/{id}
 */
export interface TermSetsItemRequestBuilder extends BaseRequestBuilder<TermSetsItemRequestBuilder> {
	/**
	 * The terms property
	 */
	get terms(): TermsRequestBuilder$1;
	/**
	 * Deletes a set without terms or fields; not the keywords set. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermSet>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TermSet | undefined>;
	/**
	 * Key and group are immutable; the keywords set stays open. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermSet>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	put(body: TermSetInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TermSet | undefined>;
	/**
	 * Deletes a set without terms or fields; not the keywords set. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Key and group are immutable; the keywords set stays open. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: TermSetInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/term-sets
 */
export interface TermSetsRequestBuilder extends BaseRequestBuilder<TermSetsRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.termSets.item collection
	 * @param id Unique identifier of the item
	 * @returns {TermSetsItemRequestBuilder}
	 */
	byId(id: string): TermSetsItemRequestBuilder;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermSetPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TermSetsRequestBuilderGetQueryParameters> | undefined): Promise<TermSetPage | undefined>;
	/**
	 * Requires taxonomy.manage; not in the system group. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermSet>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TermSetInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<TermSet | undefined>;
	/**
	 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TermSetsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Requires taxonomy.manage; not in the system group. Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.manage. Workspace membership and resource ACLs do not govern taxonomy.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TermSetInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 *  Organization taxonomy is shared across workspaces. Required module scopes: taxonomy.read. Workspace membership and resource ACLs do not govern taxonomy.
 */
export interface TermSetsRequestBuilderGetQueryParameters {
	/**
	 * Only the sets of this group.
	 */
	groupId?: string;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
interface GroupsRequestBuilder$2 extends BaseRequestBuilder<GroupsRequestBuilder$2> {
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<QueryGroupPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: ViewQueryRequest, requestConfiguration?: RequestConfiguration<GroupsRequestBuilderPostQueryParameters$2> | undefined): Promise<QueryGroupPage | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ViewQueryRequest, requestConfiguration?: RequestConfiguration<GroupsRequestBuilderPostQueryParameters$2> | undefined): RequestInformation;
}
interface GroupsRequestBuilderPostQueryParameters$2 {
	/**
	 * Continuation from @odata.nextLink; resend the same query body.
	 */
	skiptoken?: string;
	top?: number;
}
interface QueryRequestBuilder$2 extends BaseRequestBuilder<QueryRequestBuilder$2> {
	/**
	 * The groups property
	 */
	get groups(): GroupsRequestBuilder$2;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<QueryResult>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: ViewQueryRequest, requestConfiguration?: RequestConfiguration<QueryRequestBuilderPostQueryParameters$2> | undefined): Promise<QueryResult | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Pagination is specified by URL $top/$skiptoken. Repeat the same POST body at @odata.nextLink.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: ViewQueryRequest, requestConfiguration?: RequestConfiguration<QueryRequestBuilderPostQueryParameters$2> | undefined): RequestInformation;
}
interface QueryRequestBuilderPostQueryParameters$2 {
	/**
	 * Continuation from @odata.nextLink; resend the same query body.
	 */
	skiptoken?: string;
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/views/{id}
 */
export interface ViewsItemRequestBuilder extends BaseRequestBuilder<ViewsItemRequestBuilder> {
	/**
	 * The query property
	 */
	get query(): QueryRequestBuilder$2;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 412 status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ListView>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ListView | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Collection-managed saved view. Columns use field keys and $id/$name/$keywords; empty columns default to $name. PUT replaces the definition. Layout is a client hint.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<ListView>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 428 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	put(body: ViewInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<ListView | undefined>;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 *  Required module scopes: list.read. Resource ACLs and any additional use-case checks also apply.
	 * @param body Collection-managed saved view. Columns use field keys and $id/$name/$keywords; empty columns default to $name. PUT replaces the definition. Layout is a client hint.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPutRequestInformation(body: ViewInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface ViewsRequestBuilder$1 extends BaseRequestBuilder<ViewsRequestBuilder$1> {
	/**
	 * Gets an item from the PaperGo.v1.views.item collection
	 * @param id Unique identifier of the item
	 * @returns {ViewsItemRequestBuilder}
	 */
	byId(id: string): ViewsItemRequestBuilder;
}
interface KeywordsRequestBuilder$1 extends BaseRequestBuilder<KeywordsRequestBuilder$1> {
	/**
	 * Shared keywords used in this workspace, with names and counts of distinct readable resources. Includes intrinsic keywords and keywords fields on the visible head/published surface. Sorted by normalized name and ID; follow the opaque continuation cursor.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<WorkspaceKeywordCountPage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<KeywordsRequestBuilderGetQueryParameters$1> | undefined): Promise<WorkspaceKeywordCountPage | undefined>;
	/**
	 * Shared keywords used in this workspace, with names and counts of distinct readable resources. Includes intrinsic keywords and keywords fields on the visible head/published surface. Sorted by normalized name and ID; follow the opaque continuation cursor.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<KeywordsRequestBuilderGetQueryParameters$1> | undefined): RequestInformation;
}
interface KeywordsRequestBuilderGetQueryParameters$1 {
	archive?: ArchiveSelection;
	/**
	 * Only keywords used in this list or library.
	 */
	collectionId?: string;
	/**
	 * Only keywords starting with this text.
	 */
	prefix?: string;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/workspaces/{id}/members/{member}
 */
export interface WithMemberItemRequestBuilder extends BaseRequestBuilder<WithMemberItemRequestBuilder> {
	/**
	 * Owner access is required. The last owner cannot be removed. No If-Match is needed. A missing member or inaccessible workspace returns 404. Successful removal delivers permissions.changed (action=member.remove, member_subject, previous_role, empty role) to every item and folder of the workspace. Failed removals raise no event.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	delete(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * Owner access is required. The last owner cannot be removed. No If-Match is needed. A missing member or inaccessible workspace returns 404. Successful removal delivers permissions.changed (action=member.remove, member_subject, previous_role, empty role) to every item and folder of the workspace. Failed removals raise no event.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toDeleteRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export interface MembersGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: WorkspaceMember[] | null;
}
/**
 * Builds and executes requests for operations under /v1/workspaces/{id}/members
 */
export interface MembersRequestBuilder extends BaseRequestBuilder<MembersRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.workspaces.item.members.item collection
	 * @param member URL-encoded authenticated subject.
	 * @returns {WithMemberItemRequestBuilder}
	 */
	byMember(member: string): WithMemberItemRequestBuilder;
	/**
	 * Workspace read access is required. Subjects are sorted ascending. Invisible workspaces return 404.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<MembersGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<MembersGetResponse | undefined>;
	/**
	 * Owner access is required. Repeating the same subject/role is a no-op. The last owner cannot be demoted. Membership changes do not consume the workspace metadata ETag. Subject registration/active-user validation belongs to the external identity provider. Membership additions and role changes deliver permissions.changed (action=member.set, member_subject, previous_role, role) to every item and folder of the workspace. Empty previous_role means no prior membership. Repeated assignments raise no event.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: SetWorkspaceMember, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<void>;
	/**
	 * Workspace read access is required. Subjects are sorted ascending. Invisible workspaces return 404.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
	/**
	 * Owner access is required. Repeating the same subject/role is a no-op. The last owner cannot be demoted. Membership changes do not consume the workspace metadata ETag. Subject registration/active-user validation belongs to the external identity provider. Membership additions and role changes deliver permissions.changed (action=member.set, member_subject, previous_role, role) to every item and folder of the workspace. Empty previous_role means no prior membership. Repeated assignments raise no event.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SetWorkspaceMember, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
export type GetScopeQueryParameterType = (typeof GetScopeQueryParameterTypeObject)[keyof typeof GetScopeQueryParameterTypeObject];
interface ItemsRequestBuilder$2 extends BaseRequestBuilder<ItemsRequestBuilder$2> {
	/**
	 * Pages unique readable endpoints of qualifying anchored relationships, including external peers. q is a case-insensitive item-name prefix on the selected surface. item_id locates one qualifying endpoint. With neither a nonempty q nor item_id, returns an empty page. Sorts by lower-case name and ID.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RelationshipEndpointPage>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<ItemsRequestBuilderGetQueryParameters> | undefined): Promise<RelationshipEndpointPage | undefined>;
	/**
	 * Pages unique readable endpoints of qualifying anchored relationships, including external peers. q is a case-insensitive item-name prefix on the selected surface. item_id locates one qualifying endpoint. With neither a nonempty q nor item_id, returns an empty page. Sorts by lower-case name and ID.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<ItemsRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Pages unique readable endpoints of qualifying anchored relationships, including external peers. q is a case-insensitive item-name prefix on the selected surface. item_id locates one qualifying endpoint. With neither a nonempty q nor item_id, returns an empty page. Sorts by lower-case name and ID.
 */
export interface ItemsRequestBuilderGetQueryParameters {
	/**
	 * Require an endpoint in this list or library of the starting workspace.
	 */
	collectionId?: Guid;
	directed?: boolean;
	/**
	 * Focus on an incident item, including an external endpoint. Results still touch the workspace in the path. Incoming/outgoing direction requires this parameter.
	 */
	itemId?: Guid;
	/**
	 * Search text, limited to 256 UTF-8 bytes.
	 */
	q?: string;
	/**
	 * All connections, both endpoints in this workspace, or an endpoint outside it.
	 */
	scope?: GetScopeQueryParameterType;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
	/**
	 * Relationship term ID or label, including synonyms and localized labels.
	 */
	type?: string;
	/**
	 * Select untyped links only. Cannot be combined with type.
	 */
	untyped?: boolean;
}
declare const GetScopeQueryParameterTypeObject: {
	readonly All: "all";
	readonly Internal: "internal";
	readonly External: "external";
};
type GetDirectionQueryParameterType$1 = (typeof GetDirectionQueryParameterTypeObject$1)[keyof typeof GetDirectionQueryParameterTypeObject$1];
type GetScopeQueryParameterType$1 = (typeof GetScopeQueryParameterTypeObject$1)[keyof typeof GetScopeQueryParameterTypeObject$1];
/**
 * Builds and executes requests for operations under /v1/workspaces/{id}/relationships/neighborhood
 */
export interface NeighborhoodRequestBuilder extends BaseRequestBuilder<NeighborhoodRequestBuilder> {
	/**
	 * Requires item_id. Reads a bounded ID-ordered window of incident relationships, always touching the starting workspace. Returns at most 25 nodes and 50 edges, with a truncation flag when connections are omitted. Does not expand automatically beyond one hop.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<RelationshipNeighborhood>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<NeighborhoodRequestBuilderGetQueryParameters> | undefined): Promise<RelationshipNeighborhood | undefined>;
	/**
	 * Requires item_id. Reads a bounded ID-ordered window of incident relationships, always touching the starting workspace. Returns at most 25 nodes and 50 edges, with a truncation flag when connections are omitted. Does not expand automatically beyond one hop.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<NeighborhoodRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Requires item_id. Reads a bounded ID-ordered window of incident relationships, always touching the starting workspace. Returns at most 25 nodes and 50 edges, with a truncation flag when connections are omitted. Does not expand automatically beyond one hop.
 */
export interface NeighborhoodRequestBuilderGetQueryParameters {
	/**
	 * Require an endpoint in this list or library of the starting workspace.
	 */
	collectionId?: Guid;
	directed?: boolean;
	/**
	 * Incoming and outgoing select directed edges relative to item_id; symmetric selects undirected edges.
	 */
	direction?: GetDirectionQueryParameterType$1;
	filter?: string;
	/**
	 * Focus on an incident item, including an external endpoint. Results still touch the workspace in the path. Incoming/outgoing direction requires this parameter.
	 */
	itemId?: Guid;
	/**
	 * All connections, both endpoints in this workspace, or an endpoint outside it.
	 */
	scope?: GetScopeQueryParameterType$1;
	/**
	 * Relationship term ID or label, including synonyms and localized labels.
	 */
	type?: string;
	/**
	 * Select untyped links only. Cannot be combined with type.
	 */
	untyped?: boolean;
}
declare const GetDirectionQueryParameterTypeObject$1: {
	readonly Both: "both";
	readonly Incoming: "incoming";
	readonly Outgoing: "outgoing";
	readonly Symmetric: "symmetric";
};
declare const GetScopeQueryParameterTypeObject$1: {
	readonly All: "all";
	readonly Internal: "internal";
	readonly External: "external";
};
type GetScopeQueryParameterType$2 = (typeof GetScopeQueryParameterTypeObject$2)[keyof typeof GetScopeQueryParameterTypeObject$2];
/**
 * Builds and executes requests for operations under /v1/workspaces/{id}/relationships/types
 */
export interface TypesRequestBuilder extends BaseRequestBuilder<TypesRequestBuilder> {
	/**
	 * Pages types having a readable, visible incident relationship in the anchored workspace. q matches names, localized labels and synonyms as literal case-insensitive substrings. Sorts by lower-case name and ID; no exact usage totals are calculated.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<WorkspaceRelationshipTypePage>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<TypesRequestBuilderGetQueryParameters> | undefined): Promise<WorkspaceRelationshipTypePage | undefined>;
	/**
	 * Pages types having a readable, visible incident relationship in the anchored workspace. q matches names, localized labels and synonyms as literal case-insensitive substrings. Sorts by lower-case name and ID; no exact usage totals are calculated.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TypesRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Pages types having a readable, visible incident relationship in the anchored workspace. q matches names, localized labels and synonyms as literal case-insensitive substrings. Sorts by lower-case name and ID; no exact usage totals are calculated.
 */
export interface TypesRequestBuilderGetQueryParameters {
	/**
	 * Require an endpoint in this list or library of the starting workspace.
	 */
	collectionId?: Guid;
	/**
	 * Search text, limited to 256 UTF-8 bytes.
	 */
	q?: string;
	/**
	 * All connections, both endpoints in this workspace, or an endpoint outside it.
	 */
	scope?: GetScopeQueryParameterType$2;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
declare const GetScopeQueryParameterTypeObject$2: {
	readonly All: "all";
	readonly Internal: "internal";
	readonly External: "external";
};
type GetDirectionQueryParameterType$2 = (typeof GetDirectionQueryParameterTypeObject$2)[keyof typeof GetDirectionQueryParameterTypeObject$2];
type GetScopeQueryParameterType$3 = (typeof GetScopeQueryParameterTypeObject$3)[keyof typeof GetScopeQueryParameterTypeObject$3];
interface RelationshipsRequestBuilder$1 extends BaseRequestBuilder<RelationshipsRequestBuilder$1> {
	/**
	 * The items property
	 */
	get items(): ItemsRequestBuilder$2;
	/**
	 * The neighborhood property
	 */
	get neighborhood(): NeighborhoodRequestBuilder;
	/**
	 * The types property
	 */
	get types(): TypesRequestBuilder;
	/**
	 * Both endpoints must be readable on their selected content surface. Returns compact endpoint navigation summaries and resolved relationship labels; load full metadata through the per-item relationship read. All results touch the starting workspace even when item_id belongs elsewhere. Filter scalar attributes/key, id or directed using eq/ne and numeric gt/ge/lt/le, and/or/not and parentheses. Filters are limited to 4096 bytes, 32 nodes and depth 6. Example: attributes/amount gt 100. Required module scope: list.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<WorkspaceRelationshipPage>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<RelationshipsRequestBuilderGetQueryParameters$1> | undefined): Promise<WorkspaceRelationshipPage | undefined>;
	/**
	 * Both endpoints must be readable on their selected content surface. Returns compact endpoint navigation summaries and resolved relationship labels; load full metadata through the per-item relationship read. All results touch the starting workspace even when item_id belongs elsewhere. Filter scalar attributes/key, id or directed using eq/ne and numeric gt/ge/lt/le, and/or/not and parentheses. Filters are limited to 4096 bytes, 32 nodes and depth 6. Example: attributes/amount gt 100. Required module scope: list.read.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<RelationshipsRequestBuilderGetQueryParameters$1> | undefined): RequestInformation;
}
interface RelationshipsRequestBuilderGetQueryParameters$1 {
	/**
	 * Require an endpoint in this list or library of the starting workspace.
	 */
	collectionId?: Guid;
	directed?: boolean;
	/**
	 * Incoming and outgoing select directed edges relative to item_id; symmetric selects undirected edges.
	 */
	direction?: GetDirectionQueryParameterType$2;
	filter?: string;
	/**
	 * Focus on an incident item, including an external endpoint. Results still touch the workspace in the path. Incoming/outgoing direction requires this parameter.
	 */
	itemId?: Guid;
	/**
	 * All connections, both endpoints in this workspace, or an endpoint outside it.
	 */
	scope?: GetScopeQueryParameterType$3;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
	/**
	 * Relationship term ID or label, including synonyms and localized labels.
	 */
	type?: string;
	/**
	 * Select untyped links only. Cannot be combined with type.
	 */
	untyped?: boolean;
}
declare const GetDirectionQueryParameterTypeObject$2: {
	readonly Both: "both";
	readonly Incoming: "incoming";
	readonly Outgoing: "outgoing";
	readonly Symmetric: "symmetric";
};
declare const GetScopeQueryParameterTypeObject$3: {
	readonly All: "all";
	readonly Internal: "internal";
	readonly External: "external";
};
/**
 * Builds and executes requests for operations under /v1/workspaces/{id}/search/sync
 */
export interface SyncRequestBuilder extends BaseRequestBuilder<SyncRequestBuilder> {
	/**
	 * Queues a search.sync operation that rewrites the search documents of resources of the workspace from their current state, and removes those of resources that are gone. The search worker runs queued syncs together, up to 200 at a time, outside any database transaction; syncing again is harmless. Its result counts what the batch wrote and removed (SearchSyncResult). Requires write access to the workspace; a resource of another workspace is refused. Served when the deployment runs search.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Operation>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	post(body: SearchSync, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Operation | undefined>;
	/**
	 * Queues a search.sync operation that rewrites the search documents of resources of the workspace from their current state, and removes those of resources that are gone. The search worker runs queued syncs together, up to 200 at a time, outside any database transaction; syncing again is harmless. Its result counts what the batch wrote and removed (SearchSyncResult). Requires write access to the workspace; a resource of another workspace is refused. Served when the deployment runs search.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SearchSync, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 * Builds and executes requests for operations under /v1/workspaces/{id}/search/term-entries
 */
export interface TermEntriesRequestBuilder extends BaseRequestBuilder<TermEntriesRequestBuilder> {
	/**
	 * Lists, 500 at a time, the resources of the workspace, or of one of its collections, holding any of the terms or the terms merged into them: the entries a relabel or merge changes. Requires manage access to the workspace.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TermEntries>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TermEntriesRequestBuilderGetQueryParameters> | undefined): Promise<TermEntries | undefined>;
	/**
	 * Lists, 500 at a time, the resources of the workspace, or of one of its collections, holding any of the terms or the terms merged into them: the entries a relabel or merge changes. Requires manage access to the workspace.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TermEntriesRequestBuilderGetQueryParameters> | undefined): RequestInformation;
}
/**
 * Lists, 500 at a time, the resources of the workspace, or of one of its collections, holding any of the terms or the terms merged into them: the entries a relabel or merge changes. Requires manage access to the workspace.
 */
export interface TermEntriesRequestBuilderGetQueryParameters {
	/**
	 * The next of the previous page.
	 */
	after?: string;
	collectionId?: string;
	/**
	 * 1 to 20 terms.
	 */
	termId?: string[];
}
interface SearchRequestBuilder$2 extends BaseRequestBuilder<SearchRequestBuilder$2> {
	/**
	 * The sync property
	 */
	get sync(): SyncRequestBuilder;
	/**
	 * The termEntries property
	 */
	get termEntries(): TermEntriesRequestBuilder;
}
interface ExportRequestBuilder$2 extends BaseRequestBuilder<ExportRequestBuilder$2> {
	/**
	 * Export portable shared definitions (workspace manage required)
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolderPackage>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SmartFolderPackage | undefined>;
	/**
	 * Export portable shared definitions (workspace manage required)
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface ImportRequestBuilder$2 extends BaseRequestBuilder<ImportRequestBuilder$2> {
	/**
	 * Atomically merge portable shared definitions (workspace manage required)
	 * @param body Shared definitions only; personal folders and ownership IDs are excluded. Imports merge by exact unique name, preserve IDs and leave other definitions intact. Target taxonomy must exist. Any invalid entry rolls back the entire package. Identical reapplication changes no versions or audit records.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SmartFolderImportResult>}
	 * @throws {Problem} error when the service returns a 412 status code
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: SmartFolderPackage, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SmartFolderImportResult | undefined>;
	/**
	 * Atomically merge portable shared definitions (workspace manage required)
	 * @param body Shared definitions only; personal folders and ownership IDs are excluded. Imports merge by exact unique name, preserve IDs and leave other definitions intact. Target taxonomy must exist. Any invalid entry rolls back the entire package. Identical reapplication changes no versions or audit records.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: SmartFolderPackage, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface SmartFoldersRequestBuilder$1 extends BaseRequestBuilder<SmartFoldersRequestBuilder$1> {
	/**
	 * The export property
	 */
	get exportEscaped(): ExportRequestBuilder$2;
	/**
	 * The import property
	 */
	get importEscaped(): ImportRequestBuilder$2;
}
type GetKindQueryParameterType$3 = (typeof GetKindQueryParameterTypeObject$3)[keyof typeof GetKindQueryParameterTypeObject$3];
type GetStatusQueryParameterType$1 = (typeof GetStatusQueryParameterTypeObject$1)[keyof typeof GetStatusQueryParameterTypeObject$1];
interface TasksRequestBuilder$2 extends BaseRequestBuilder<TasksRequestBuilder$2> {
	/**
	 * A workspace's tasks, for its owners.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<TaskList>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TasksRequestBuilderGetQueryParameters$1> | undefined): Promise<TaskList | undefined>;
	/**
	 * Creates a task. The creator needs read access to the workspace, and write access to the item when it concerns one. Assignees are recipient entries; none means the workspace's owners. Unknown recipients fail with their names. A task a workflow run creates is that workflow's: its operation_id is the operation its answer ends, which the run waits on, it may review a staged file of the run, and it is cancelled with the run.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Task>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TaskCreate, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Task | undefined>;
	/**
	 * A workspace's tasks, for its owners.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TasksRequestBuilderGetQueryParameters$1> | undefined): RequestInformation;
	/**
	 * Creates a task. The creator needs read access to the workspace, and write access to the item when it concerns one. Assignees are recipient entries; none means the workspace's owners. Unknown recipients fail with their names. A task a workflow run creates is that workflow's: its operation_id is the operation its answer ends, which the run waits on, it may review a staged file of the run, and it is cancelled with the run.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TaskCreate, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
interface TasksRequestBuilderGetQueryParameters$1 {
	key?: string;
	kind?: GetKindQueryParameterType$3;
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	status?: GetStatusQueryParameterType$1;
	top?: number;
}
declare const GetKindQueryParameterTypeObject$3: {
	readonly Task: "task";
	readonly Approval: "approval";
};
declare const GetStatusQueryParameterTypeObject$1: {
	readonly Open: "open";
	readonly Completed: "completed";
	readonly Cancelled: "cancelled";
	readonly Expired: "expired";
};
interface TemplatesRequestBuilder$2 extends BaseRequestBuilder<TemplatesRequestBuilder$2> {
	/**
	 *  Required module scopes: contentType.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SchemaTemplatePage>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	get(requestConfiguration?: RequestConfiguration<TemplatesRequestBuilderGetQueryParameters> | undefined): Promise<SchemaTemplatePage | undefined>;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<SchemaTemplate>}
	 * @throws {Problem} error when the service returns a 400 status code
	 * @throws {Problem} error when the service returns a 401 status code
	 * @throws {Problem} error when the service returns a 403 status code
	 * @throws {Problem} error when the service returns a 404 status code
	 * @throws {Problem} error when the service returns a 409 status code
	 * @throws {Problem} error when the service returns a 413 status code
	 * @throws {Problem} error when the service returns a 415 status code
	 * @throws {Problem} error when the service returns a 422 status code
	 * @throws {Problem} error when the service returns a 500 status code
	 */
	post(body: TemplateInput, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<SchemaTemplate | undefined>;
	/**
	 *  Required module scopes: contentType.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<TemplatesRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 *  Required module scopes: contentType.manage. Resource ACLs and any additional use-case checks also apply.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: TemplateInput, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 *  Required module scopes: contentType.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface TemplatesRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1/workspaces/{id}
 */
export interface WorkspacesItemRequestBuilder extends BaseRequestBuilder<WorkspacesItemRequestBuilder> {
	/**
	 * The keywords property
	 */
	get keywords(): KeywordsRequestBuilder$1;
	/**
	 * The members property
	 */
	get members(): MembersRequestBuilder;
	/**
	 * The relationships property
	 */
	get relationships(): RelationshipsRequestBuilder$1;
	/**
	 * The search property
	 */
	get search(): SearchRequestBuilder$2;
	/**
	 * The smartFolders property
	 */
	get smartFolders(): SmartFoldersRequestBuilder$1;
	/**
	 * The tasks property
	 */
	get tasks(): TasksRequestBuilder$2;
	/**
	 * The templates property
	 */
	get templates(): TemplatesRequestBuilder$2;
}
export interface WorkspacesGetResponse extends Parsable {
	/**
	 * The OdataNextLink property
	 */
	odataNextLink?: string | null;
	/**
	 * The value property
	 */
	value?: Resource[] | null;
}
export interface WorkspacesPostRequestBody extends Parsable {
	/**
	 * Workspace description. Only workspaces accept this property; an empty string clears it.
	 */
	description?: string | null;
	/**
	 * The keywords property
	 */
	keywords?: string[] | null;
	/**
	 * The name property
	 */
	name?: string | null;
}
/**
 * Builds and executes requests for operations under /v1/workspaces
 */
export interface WorkspacesRequestBuilder extends BaseRequestBuilder<WorkspacesRequestBuilder> {
	/**
	 * Gets an item from the PaperGo.v1.workspaces.item collection
	 * @param id Unique identifier of the item
	 * @returns {WorkspacesItemRequestBuilder}
	 */
	byId(id: Guid): WorkspacesItemRequestBuilder;
	/**
	 *  Required module scopes: workspace.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<WorkspacesGetResponse>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	get(requestConfiguration?: RequestConfiguration<WorkspacesRequestBuilderGetQueryParameters> | undefined): Promise<WorkspacesGetResponse | undefined>;
	/**
	 * Creates a workspace with the authenticated subject as its first owner. Name is trimmed (1..200 characters); description is at most 2000 characters. Required module scopes: workspace.create. Resource ACLs and any additional use-case checks also apply.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {Promise<Resource>}
	 * @throws {Problem} error when the service returns a 4XX or 5XX status code
	 */
	post(body: WorkspacesPostRequestBody, requestConfiguration?: RequestConfiguration<object> | undefined): Promise<Resource | undefined>;
	/**
	 *  Required module scopes: workspace.read. Resource ACLs and any additional use-case checks also apply.
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toGetRequestInformation(requestConfiguration?: RequestConfiguration<WorkspacesRequestBuilderGetQueryParameters> | undefined): RequestInformation;
	/**
	 * Creates a workspace with the authenticated subject as its first owner. Name is trimmed (1..200 characters); description is at most 2000 characters. Required module scopes: workspace.create. Resource ACLs and any additional use-case checks also apply.
	 * @param body The request body
	 * @param requestConfiguration Configuration for the request such as headers, query parameters, and middleware options.
	 * @returns {RequestInformation}
	 */
	toPostRequestInformation(body: WorkspacesPostRequestBody, requestConfiguration?: RequestConfiguration<object> | undefined): RequestInformation;
}
/**
 *  Required module scopes: workspace.read. Resource ACLs and any additional use-case checks also apply.
 */
export interface WorkspacesRequestBuilderGetQueryParameters {
	/**
	 * Opaque continuation token supplied by @odata.nextLink.
	 */
	skiptoken?: string;
	/**
	 * Page size; values are clamped to 1–500. Missing or invalid values use 50.
	 */
	top?: number;
}
/**
 * Builds and executes requests for operations under /v1
 */
export interface V1RequestBuilder extends BaseRequestBuilder<V1RequestBuilder> {
	/**
	 * The ai property
	 */
	get ai(): AiRequestBuilder;
	/**
	 * The archive property
	 */
	get archive(): ArchiveRequestBuilder;
	/**
	 * The artifacts property
	 */
	get artifacts(): ArtifactsRequestBuilder;
	/**
	 * The audit property
	 */
	get audit(): AuditRequestBuilder;
	/**
	 * The collectionTemplates property
	 */
	get collectionTemplates(): CollectionTemplatesRequestBuilder;
	/**
	 * The contentTypes property
	 */
	get contentTypes(): ContentTypesRequestBuilder;
	/**
	 * The fields property
	 */
	get fields(): FieldsRequestBuilder;
	/**
	 * The items property
	 */
	get items(): ItemsRequestBuilder;
	/**
	 * The keywords property
	 */
	get keywords(): KeywordsRequestBuilder;
	/**
	 * The operations property
	 */
	get operations(): OperationsRequestBuilder;
	/**
	 * The people property
	 */
	get people(): PeopleRequestBuilder;
	/**
	 * The relationshipTypes property
	 */
	get relationshipTypes(): RelationshipTypesRequestBuilder;
	/**
	 * The resources property
	 */
	get resources(): ResourcesRequestBuilder;
	/**
	 * The search property
	 */
	get search(): SearchRequestBuilder$1;
	/**
	 * The smartFolders property
	 */
	get smartFolders(): SmartFoldersRequestBuilder;
	/**
	 * The stagedFiles property
	 */
	get stagedFiles(): StagedFilesRequestBuilder;
	/**
	 * The tasks property
	 */
	get tasks(): TasksRequestBuilder$1;
	/**
	 * The taxonomy property
	 */
	get taxonomy(): TaxonomyRequestBuilder;
	/**
	 * The templates property
	 */
	get templates(): TemplatesRequestBuilder$1;
	/**
	 * The termGroups property
	 */
	get termGroups(): TermGroupsRequestBuilder;
	/**
	 * The terms property
	 */
	get terms(): TermsRequestBuilder;
	/**
	 * The termSets property
	 */
	get termSets(): TermSetsRequestBuilder;
	/**
	 * The views property
	 */
	get views(): ViewsRequestBuilder$1;
	/**
	 * The workspaces property
	 */
	get workspaces(): WorkspacesRequestBuilder;
}
/**
 * The main entry point of the SDK, exposes the configuration and the fluent API.
 */
export interface PaperGoClient extends BaseRequestBuilder<PaperGoClient> {
	/**
	 * The v1 property
	 */
	get v1(): V1RequestBuilder;
}
/**
 * When a workflow runs. Item and folder events, schedules, manual starts,
 * task events, permission changes, term changes, and events of other
 * workflows (wf.{key}.completed) are trigger types; docs/workflows.md lists
 * them with their options.
 */
export interface Trigger {
	type: string;
	disabled?: boolean;
	collection_id?: string;
	content_type_id?: string;
	cron?: string;
	time_zone?: string;
	surface?: "head" | "published";
	selection?: "per_item" | "selection";
	terms?: string[];
	term_change?: "present" | "added" | "removed";
	sources?: string[];
	media_types?: string[];
	task_key?: string;
	actions?: string[];
}
/** What a start does while a run on the same item is active. */
export type Concurrency = "parallel" | "skip" | "replace" | "coalesce";
/** The event that started a run. */
export interface RunEvent {
	id: string;
	type: string;
	workspace_id: string;
	collection_id?: string;
	resource_id?: string;
	actor: string;
	depth: number;
	data: Record<string, unknown>;
}
/** Content captured once for the triggering revision; use the API for current state and ETags. */
export interface TriggerResource {
	readonly id: string;
	readonly workspace_id: string;
	readonly collection_id: string;
	readonly revision_id: string;
	readonly revision_number: number;
	readonly content_type_id: string;
	readonly name: string;
	readonly keywords: readonly string[];
	/** Plain JSON field values. Absent when they would exceed the snapshot's 64 KiB budget. */
	readonly values?: Readonly<Record<string, unknown>>;
	/** True when values were excluded to keep the snapshot small; API reads return current state. */
	readonly values_omitted: boolean;
}
/** Metadata of the file version that caused the event; file bytes stay behind the API. */
export interface TriggerFile {
	readonly id: string;
	readonly version: number;
	readonly filename: string;
	readonly content_type: string;
	readonly size: number;
}
/** One manually selected item, in launch order. */
export interface TriggerSelection {
	readonly id: string;
	/** Readable content at dispatch; absent if inaccessible or deleted. */
	readonly snapshot?: TriggerResource;
}
/** A manual launch, including test runs of a script with form values and selected items. */
export interface ManualTrigger<I = unknown> {
	readonly kind: "manual";
	/** Submitted launch form values; the same object as run(ctx, input)'s second argument. */
	readonly input: I;
	/** The primary selected item's readable snapshot, when available. */
	readonly item?: TriggerResource;
	/** Every selected item in launch order, including the primary item. Empty for standalone starts. */
	readonly items: readonly TriggerSelection[];
}
/** Narrow on kind to access manual inputs and selection, or the triggering item, folder or file. */
export type TriggerObject<I = unknown> = ManualTrigger<I> | {
	readonly kind: "item";
	readonly item: TriggerResource;
} | {
	readonly kind: "folder";
	readonly folder: TriggerResource;
} | {
	readonly kind: "file";
	readonly item: TriggerResource;
	readonly file: TriggerFile;
};
/** The run being executed. */
export interface Run<P = Record<string, unknown>, I = unknown> {
	id: string;
	workflow: {
		id: string;
		key: string;
		version: number;
	};
	workspace_id: string;
	/** The run's item; a selection run's primary item. */
	item_id?: string;
	/** A selection run's items, in order. */
	items: string[];
	event: RunEvent;
	/** Stable trigger snapshot, when the author can read its content. */
	trigger?: TriggerObject<I>;
	/** Who caused the run. */
	actor: string;
	params: P;
}
export interface Context<P = Record<string, unknown>, I = unknown> {
	readonly runId: string;
	readonly run: Run<P, I>;
	/** The workflow's parameters, such as a built-in's settings. */
	readonly params: P;
	/** The run's item, when it has one. */
	readonly itemId?: string;
	/**
	 * Triggering revision content, persisted for replay (up to 64 KiB).
	 * File events include file metadata and their item. Manual/test starts
	 * expose input, primary item and the complete ordered selection.
	 * Other standalone, deleted, purged or inaccessible resources have no snapshot.
	 * Coalescing workflows should use the API when they need current state.
	 */
	readonly trigger?: TriggerObject<I>;
	/** The PaperGo REST API, acting as the workflow version's author. Each call is one exactly-once step. */
	readonly api: PaperGoClient;
	/**
	 * Waits until operations have finished and returns them as they ended:
	 * operations the API returned, or their IDs, such as a task's
	 * operation_id. The run is unloaded while it waits. Waiting for several
	 * at once lets them run in parallel. A failed or cancelled operation
	 * throws an OperationError.
	 */
	wait(operation: Operation | string): Promise<Operation>;
	wait(operations: (Operation | string)[]): Promise<Operation[]>;
	/** Unloads the run for ms milliseconds, or until a time. */
	sleep(duration: number | {
		until: Date | string;
	}): Promise<void>;
	/** Writes to the run's log. A replay does not repeat earlier entries. */
	log(...values: unknown[]): void;
	warn(...values: unknown[]): void;
	error(...values: unknown[]): void;
}
export interface Workflow<I = unknown, O = unknown, P = Record<string, unknown>> {
	/** When the workflow runs; a function of the parameters for built-ins. */
	triggers?: Trigger[] | ((params: P) => Trigger[]);
	/** A filter items must meet, as in POST /v1/resources/{id}/query; a function of the parameters for built-ins. */
	condition?: Record<string, unknown> | ((params: P) => Record<string, unknown>);
	/** The launch form of manual starts; its values are the run's input. */
	input_schema?: Record<string, unknown>;
	/** The form of what run returns, checked when the run completes. */
	output_schema?: Record<string, unknown>;
	concurrency?: Concurrency;
	run(ctx: Context<P, I>, input: I): O | Promise<O>;
}
/** Declares a workflow. A script's default export must be one. */
export declare function workflow<I = unknown, O = unknown, P = Record<string, unknown>>(definition: Workflow<I, O, P>): Workflow<I, O, P>;
/** An operation that ended without succeeding. */
export declare class OperationError extends Error {
	readonly operation: Operation;
	readonly operations: Operation[];
	constructor(operation: Operation, operations: Operation[]);
}
/** Converts Kiota untyped values, such as an operation's result, to plain JSON values. */
export declare function plain(value: UntypedNode | null | undefined): unknown;
/**
 * Wraps a free-form JSON object for a request field the API leaves open,
 * such as an item's values or an output schema.
 */
export declare function json<T extends Record<string, unknown>>(value: T): {
	additionalData: T;
};
/**
 * Runs definition for the engine; scripts do not call it. run is the run's
 * JSON with its input; the promise resolves to the output as JSON text.
 */
export declare function __start(definition: unknown, run: string): Promise<string>;
/**
 * Returns, as JSON text, what the engine reads when a version is saved:
 * the triggers for params and the other settings. run is not called.
 */
export declare function __manifest(definition: unknown, params: string): string;

declare namespace models {
	export { ApplyTemplateInput, ArchiveEntry, ArchiveEntry_duplicate_policy, ArchiveEntry_duplicate_policyObject, ArchiveEntry_kind, ArchiveEntry_kindObject, ArchiveEntry_system_key, ArchiveEntry_system_keyObject, ArchiveEntry_values, ArchiveEntry_versioning, ArchiveEntry_versioningObject, ArchiveSelection, ArchiveSelectionObject, Artifact, Artifact_metrics, Artifact_sources, AttachContentTypeInput, AuditEvent, AuditEvent_details, AuditPage, Blob, Blob_content_type, Blob_content_typeObject, BulkCreate, BulkCreate_values, BulkOperation, BulkOperationMember1, BulkOperationMember2, BulkOperationMember3, BulkOperationMember4, BulkOperationMember5, BulkOperationMember6, BulkOperationMember7, BulkRequest, BulkResponse, BulkResult, BulkResult_action, BulkResult_actionObject, BulkUpdate, BulkUpdate_values, CalendarOccurrence, CalendarQuery, CalendarQuery_surface, CalendarQuery_surfaceObject, ChunkText, ChunkText_chunker, ChunkText_chunkerObject, ChunkText_kind, ChunkText_kindObject, CollectionContentType, CollectionTemplate, CollectionTemplate_duplicate_policy, CollectionTemplate_duplicate_policyObject, CollectionTemplate_kind, CollectionTemplate_kindObject, CollectionTemplate_versioning, CollectionTemplate_versioningObject, CompletionRequest, CompletionRequest_execution, CompletionRequest_executionObject, CompletionRequest_output_schema, ComposePDF, ContentType, ContentTypeInput, CreateCatalogField, CreateCatalogField_type, CreateCatalogField_typeObject, CreateField, CreateField_type, CreateField_typeObject, CreateListIntegration, CreateListIntegration_configuration, CreateRelationship, CreateRelationship_metadata, CreateResource, CreateResource_duplicate_policy, CreateResource_duplicate_policyObject, CreateResource_kind, CreateResource_kindObject, CreateResource_values, CreateResource_versioning, CreateResource_versioningObject, DecisionRequest, DecisionRequest_context, DecisionRequest_data, DecisionRequest_questions, DecisionRequest_questions_options, DecisionRequest_questions_type, DecisionRequest_questions_typeObject, DeletedResource, DeltaPage, DeltaPage_value, DeltaPage_valueMember1, DeltaPage_valueMember1_Removed, DeltaPage_valueMember1_Removed_reason, DeltaPage_valueMember1_Removed_reasonObject, DocumentMetadata, DocumentMetadata_values, DocumentUpload, DuplicateDocument, FieldDefinition, FieldDefinition_index_status, FieldDefinition_index_statusObject, FieldDefinition_type, FieldDefinition_typeObject, FieldOptions, FieldOptions_search_weight, FieldOptions_search_weightObject, FileVersion, FileVersion_availability, FileVersion_availabilityObject, FilterExpr, FilterExpr_op, FilterExpr_opObject, FilterExpr_value_ref, FilterExpr_value_refObject, FilteredBulkUpdate, FilteredBulkUpdate_values, GenerationChunk, GenerationChunkPage, GenerationEnrichments, GenerationEnrichments_enrichments, ItemIntegration, ItemRevision, ItemRevision_payload, ItemText, ItemText_pages, KeywordCount, KeywordCountList, KeywordInput, ListIntegration, ListIntegration_kind, ListIntegration_kindObject, ListView, ListViewPage, ListView_system_key, ListView_system_keyObject, LocatedItem, MergeTerm, MoveItem, MoveTerm, Operation, OperationFailure, OperationPage, Operation_error_code, Operation_error_codeObject, Operation_status, Operation_statusObject, Operation_waiting, Operation_waitingObject, OptimizeImage, Permissions, Permissions_effective_grants, Permissions_effective_grants_action, Permissions_effective_grants_actionObject, Permissions_effective_grants_effect, Permissions_effective_grants_effectObject, Permissions_effective_grants_principal_type, Permissions_effective_grants_principal_typeObject, Permissions_grants, Permissions_grants_action, Permissions_grants_actionObject, Permissions_grants_effect, Permissions_grants_effectObject, Permissions_grants_principal_type, Permissions_grants_principal_typeObject, PortableSmartFolder, PortableSmartFolderDefinition, PortableSmartFolderDefinition_collection_kind, PortableSmartFolderDefinition_collection_kindObject, PortableSmartFolderDefinition_mode, PortableSmartFolderDefinition_modeObject, PortableSmartFolderDefinition_term_match, PortableSmartFolderDefinition_term_matchObject, PortableTerm, PortableTermGroup, PortableTermSet, PortableTerm_labels, Problem, Problem_existing_item, PromoteKeyword, PromoteStagedFile, PromotedFile, Publication, Publication_action, Publication_actionObject, Publication_snapshot, QueryGroup, QueryGroupPage, QueryRequest, QueryRequest_surface, QueryRequest_surfaceObject, QueryResult, QuerySpec, ReadText, ReadText_ocr, ReadText_ocrObject, ReclaimFileVersion, ReclaimedFileVersion, RecycleBinPage, Relationship, RelationshipEndpoint, RelationshipEndpointPage, RelationshipNeighborhood, RelationshipType, RelationshipTypeInput, RelationshipTypePage, RelationshipType_attributes, RelationshipView, Relationship_metadata, Resource, ResourceAccess, ResourceAccess_access, ResourceAccess_accessObject, Resource_duplicate_policy, Resource_duplicate_policyObject, Resource_kind, Resource_kindObject, Resource_system_key, Resource_system_keyObject, Resource_values, Resource_versioning, Resource_versioningObject, SchemaRevision, SchemaRevision_definition, SchemaRevision_definition_fields, SchemaRevision_definition_fields_type, SchemaRevision_definition_fields_typeObject, SchemaTemplate, SchemaTemplatePage, SchemaTemplate_definition, SearchFacetValue, SearchFacets, SearchField, SearchField_operators, SearchField_operatorsObject, SearchHit, SearchHit_kind, SearchHit_kindObject, SearchHit_matched_by, SearchHit_matched_byObject, SearchHit_surface, SearchHit_surfaceObject, SearchPage, SearchPage_mode, SearchPage_modeObject, SearchRequest, SearchRequest_kind, SearchRequest_kindObject, SearchRequest_mode, SearchRequest_modeObject, SearchRequest_surface, SearchRequest_surfaceObject, SearchStatus, SearchStatus_embeddings, SearchSync, SetWorkspaceMember, SetWorkspaceMember_role, SetWorkspaceMember_roleObject, SmartFolder, SmartFolderDefinition, SmartFolderDefinition_collection_kind, SmartFolderDefinition_collection_kindObject, SmartFolderDefinition_mode, SmartFolderDefinition_modeObject, SmartFolderDefinition_term_match, SmartFolderDefinition_term_matchObject, SmartFolderDrop, SmartFolderEntry, SmartFolderEntry_collection_kind, SmartFolderEntry_collection_kindObject, SmartFolderGroup, SmartFolderGroupBy, SmartFolderGroupBy_by, SmartFolderGroupBy_byObject, SmartFolderGroupsResult, SmartFolderImportResult, SmartFolderInput, SmartFolderPackage, SmartFolderPage, SmartFolderPreview, SmartFolderQueryRequest, SmartFolderQueryRequest_sort, SmartFolderQueryRequest_sort_direction, SmartFolderQueryRequest_sort_directionObject, SmartFolderQueryRequest_sort_field, SmartFolderQueryRequest_sort_fieldObject, SmartFolderQueryRequest_surface, SmartFolderQueryRequest_surfaceObject, SmartFolderResult, SmartFolderTermRef, SmartFolderUnclassify, SortSpec, SortSpec_direction, SortSpec_directionObject, StagedArtifact, StagedFile, StagedFile_metrics, StagedFile_sources, StagedFile_state, StagedFile_stateObject, Task, TaskAnswer, TaskAnswer_input, TaskCreate, TaskCreate_completion, TaskCreate_completionObject, TaskCreate_kind, TaskCreate_kindObject, TaskCreate_priority, TaskCreate_priorityObject, TaskCreate_review, TaskCreate_review_type, TaskCreate_review_typeObject, TaskCreate_values, TaskList, TaskOutcome, TaskOutcome_comment, TaskOutcome_commentObject, TaskOutcome_style, TaskOutcome_styleObject, TaskReassign, TaskResponseInput, TaskResponseInput_input, Task_completion, Task_completionObject, Task_input, Task_kind, Task_kindObject, Task_priority, Task_priorityObject, Task_review, Task_review_type, Task_review_typeObject, Task_source, Task_sourceObject, Task_status, Task_statusObject, Task_values, TaxonomyDecisionRequest, TaxonomyDecisionRequest_context, TaxonomyDecisionRequest_data, TaxonomyDecisionRequest_description_overrides, TaxonomyDecisionRequest_selection, TaxonomyDecisionRequest_selectionObject, TaxonomyImportResult, TaxonomyPackage, TemplateField, TemplateField_type, TemplateField_typeObject, TemplateInput, Term, TermEntries, TermGroup, TermGroupInput, TermGroupPage, TermImportResult, TermInput, TermInput_labels, TermList, TermPage, TermSet, TermSetInput, TermSetPage, Term_labels, UpdateField, UpdateListIntegration, UpdateListIntegration_configuration, UpdateRelationship, UpdateRelationship_metadata, UpdateResource, UpdateResource_duplicate_policy, UpdateResource_duplicate_policyObject, UpdateResource_values, UpdateResource_versioning, UpdateResource_versioningObject, ValidationRule, ValidationRuleMember1, ValidationRuleMember2, ValidationRuleMember2_op, ValidationRuleMember2_opObject, ViewInput, ViewInput_layout, ViewInput_layoutObject, ViewQueryRequest, ViewQueryRequest_surface, ViewQueryRequest_surfaceObject, WorkflowForm, WorkspaceKeywordCount, WorkspaceKeywordCountPage, WorkspaceMember, WorkspaceMember_role, WorkspaceMember_roleObject, WorkspaceRelationshipPage, WorkspaceRelationshipType, WorkspaceRelationshipTypePage, WorkspaceRelationshipView, createApplyTemplateInputFromDiscriminatorValue, createArchiveEntryFromDiscriminatorValue, createArchiveEntry_valuesFromDiscriminatorValue, createArtifactFromDiscriminatorValue, createArtifact_metricsFromDiscriminatorValue, createArtifact_sourcesFromDiscriminatorValue, createAttachContentTypeInputFromDiscriminatorValue, createAuditEventFromDiscriminatorValue, createAuditEvent_detailsFromDiscriminatorValue, createAuditPageFromDiscriminatorValue, createBlobFromDiscriminatorValue, createBulkCreateFromDiscriminatorValue, createBulkCreate_valuesFromDiscriminatorValue, createBulkOperationFromDiscriminatorValue, createBulkOperationMember1FromDiscriminatorValue, createBulkOperationMember2FromDiscriminatorValue, createBulkOperationMember3FromDiscriminatorValue, createBulkOperationMember4FromDiscriminatorValue, createBulkOperationMember5FromDiscriminatorValue, createBulkOperationMember6FromDiscriminatorValue, createBulkOperationMember7FromDiscriminatorValue, createBulkRequestFromDiscriminatorValue, createBulkResponseFromDiscriminatorValue, createBulkResultFromDiscriminatorValue, createBulkUpdateFromDiscriminatorValue, createBulkUpdate_valuesFromDiscriminatorValue, createCalendarOccurrenceFromDiscriminatorValue, createCalendarQueryFromDiscriminatorValue, createChunkTextFromDiscriminatorValue, createCollectionContentTypeFromDiscriminatorValue, createCollectionTemplateFromDiscriminatorValue, createCompletionRequestFromDiscriminatorValue, createCompletionRequest_output_schemaFromDiscriminatorValue, createComposePDFFromDiscriminatorValue, createContentTypeFromDiscriminatorValue, createContentTypeInputFromDiscriminatorValue, createCreateCatalogFieldFromDiscriminatorValue, createCreateFieldFromDiscriminatorValue, createCreateListIntegrationFromDiscriminatorValue, createCreateListIntegration_configurationFromDiscriminatorValue, createCreateRelationshipFromDiscriminatorValue, createCreateRelationship_metadataFromDiscriminatorValue, createCreateResourceFromDiscriminatorValue, createCreateResource_valuesFromDiscriminatorValue, createDecisionRequestFromDiscriminatorValue, createDecisionRequest_contextFromDiscriminatorValue, createDecisionRequest_dataFromDiscriminatorValue, createDecisionRequest_questionsFromDiscriminatorValue, createDecisionRequest_questions_optionsFromDiscriminatorValue, createDeletedResourceFromDiscriminatorValue, createDeltaPageFromDiscriminatorValue, createDeltaPage_valueFromDiscriminatorValue, createDeltaPage_valueMember1FromDiscriminatorValue, createDeltaPage_valueMember1_RemovedFromDiscriminatorValue, createDocumentMetadataFromDiscriminatorValue, createDocumentMetadata_valuesFromDiscriminatorValue, createDocumentUploadFromDiscriminatorValue, createDuplicateDocumentFromDiscriminatorValue, createFieldDefinitionFromDiscriminatorValue, createFieldOptionsFromDiscriminatorValue, createFileVersionFromDiscriminatorValue, createFilterExprFromDiscriminatorValue, createFilteredBulkUpdateFromDiscriminatorValue, createFilteredBulkUpdate_valuesFromDiscriminatorValue, createGenerationChunkFromDiscriminatorValue, createGenerationChunkPageFromDiscriminatorValue, createGenerationEnrichmentsFromDiscriminatorValue, createGenerationEnrichments_enrichmentsFromDiscriminatorValue, createItemIntegrationFromDiscriminatorValue, createItemRevisionFromDiscriminatorValue, createItemRevision_payloadFromDiscriminatorValue, createItemTextFromDiscriminatorValue, createItemText_pagesFromDiscriminatorValue, createKeywordCountFromDiscriminatorValue, createKeywordCountListFromDiscriminatorValue, createKeywordInputFromDiscriminatorValue, createListIntegrationFromDiscriminatorValue, createListViewFromDiscriminatorValue, createListViewPageFromDiscriminatorValue, createLocatedItemFromDiscriminatorValue, createMergeTermFromDiscriminatorValue, createMoveItemFromDiscriminatorValue, createMoveTermFromDiscriminatorValue, createOperationFailureFromDiscriminatorValue, createOperationFromDiscriminatorValue, createOperationPageFromDiscriminatorValue, createOptimizeImageFromDiscriminatorValue, createPermissionsFromDiscriminatorValue, createPermissions_effective_grantsFromDiscriminatorValue, createPermissions_grantsFromDiscriminatorValue, createPortableSmartFolderDefinitionFromDiscriminatorValue, createPortableSmartFolderFromDiscriminatorValue, createPortableTermFromDiscriminatorValue, createPortableTermGroupFromDiscriminatorValue, createPortableTermSetFromDiscriminatorValue, createPortableTerm_labelsFromDiscriminatorValue, createProblemFromDiscriminatorValue, createProblem_existing_itemFromDiscriminatorValue, createPromoteKeywordFromDiscriminatorValue, createPromoteStagedFileFromDiscriminatorValue, createPromotedFileFromDiscriminatorValue, createPublicationFromDiscriminatorValue, createPublication_snapshotFromDiscriminatorValue, createQueryGroupFromDiscriminatorValue, createQueryGroupPageFromDiscriminatorValue, createQueryRequestFromDiscriminatorValue, createQueryResultFromDiscriminatorValue, createQuerySpecFromDiscriminatorValue, createReadTextFromDiscriminatorValue, createReclaimFileVersionFromDiscriminatorValue, createReclaimedFileVersionFromDiscriminatorValue, createRecycleBinPageFromDiscriminatorValue, createRelationshipEndpointFromDiscriminatorValue, createRelationshipEndpointPageFromDiscriminatorValue, createRelationshipFromDiscriminatorValue, createRelationshipNeighborhoodFromDiscriminatorValue, createRelationshipTypeFromDiscriminatorValue, createRelationshipTypeInputFromDiscriminatorValue, createRelationshipTypePageFromDiscriminatorValue, createRelationshipType_attributesFromDiscriminatorValue, createRelationshipViewFromDiscriminatorValue, createRelationship_metadataFromDiscriminatorValue, createResourceAccessFromDiscriminatorValue, createResourceFromDiscriminatorValue, createResource_valuesFromDiscriminatorValue, createSchemaRevisionFromDiscriminatorValue, createSchemaRevision_definitionFromDiscriminatorValue, createSchemaRevision_definition_fieldsFromDiscriminatorValue, createSchemaTemplateFromDiscriminatorValue, createSchemaTemplatePageFromDiscriminatorValue, createSchemaTemplate_definitionFromDiscriminatorValue, createSearchFacetValueFromDiscriminatorValue, createSearchFacetsFromDiscriminatorValue, createSearchFieldFromDiscriminatorValue, createSearchHitFromDiscriminatorValue, createSearchPageFromDiscriminatorValue, createSearchRequestFromDiscriminatorValue, createSearchStatusFromDiscriminatorValue, createSearchStatus_embeddingsFromDiscriminatorValue, createSearchSyncFromDiscriminatorValue, createSetWorkspaceMemberFromDiscriminatorValue, createSmartFolderDefinitionFromDiscriminatorValue, createSmartFolderDropFromDiscriminatorValue, createSmartFolderEntryFromDiscriminatorValue, createSmartFolderFromDiscriminatorValue, createSmartFolderGroupByFromDiscriminatorValue, createSmartFolderGroupFromDiscriminatorValue, createSmartFolderGroupsResultFromDiscriminatorValue, createSmartFolderImportResultFromDiscriminatorValue, createSmartFolderInputFromDiscriminatorValue, createSmartFolderPackageFromDiscriminatorValue, createSmartFolderPageFromDiscriminatorValue, createSmartFolderPreviewFromDiscriminatorValue, createSmartFolderQueryRequestFromDiscriminatorValue, createSmartFolderQueryRequest_sortFromDiscriminatorValue, createSmartFolderResultFromDiscriminatorValue, createSmartFolderTermRefFromDiscriminatorValue, createSmartFolderUnclassifyFromDiscriminatorValue, createSortSpecFromDiscriminatorValue, createStagedArtifactFromDiscriminatorValue, createStagedFileFromDiscriminatorValue, createStagedFile_metricsFromDiscriminatorValue, createStagedFile_sourcesFromDiscriminatorValue, createTaskAnswerFromDiscriminatorValue, createTaskAnswer_inputFromDiscriminatorValue, createTaskCreateFromDiscriminatorValue, createTaskCreate_reviewFromDiscriminatorValue, createTaskCreate_valuesFromDiscriminatorValue, createTaskFromDiscriminatorValue, createTaskListFromDiscriminatorValue, createTaskOutcomeFromDiscriminatorValue, createTaskReassignFromDiscriminatorValue, createTaskResponseInputFromDiscriminatorValue, createTaskResponseInput_inputFromDiscriminatorValue, createTask_inputFromDiscriminatorValue, createTask_reviewFromDiscriminatorValue, createTask_valuesFromDiscriminatorValue, createTaxonomyDecisionRequestFromDiscriminatorValue, createTaxonomyDecisionRequest_contextFromDiscriminatorValue, createTaxonomyDecisionRequest_dataFromDiscriminatorValue, createTaxonomyDecisionRequest_description_overridesFromDiscriminatorValue, createTaxonomyImportResultFromDiscriminatorValue, createTaxonomyPackageFromDiscriminatorValue, createTemplateFieldFromDiscriminatorValue, createTemplateInputFromDiscriminatorValue, createTermEntriesFromDiscriminatorValue, createTermFromDiscriminatorValue, createTermGroupFromDiscriminatorValue, createTermGroupInputFromDiscriminatorValue, createTermGroupPageFromDiscriminatorValue, createTermImportResultFromDiscriminatorValue, createTermInputFromDiscriminatorValue, createTermInput_labelsFromDiscriminatorValue, createTermListFromDiscriminatorValue, createTermPageFromDiscriminatorValue, createTermSetFromDiscriminatorValue, createTermSetInputFromDiscriminatorValue, createTermSetPageFromDiscriminatorValue, createTerm_labelsFromDiscriminatorValue, createUpdateFieldFromDiscriminatorValue, createUpdateListIntegrationFromDiscriminatorValue, createUpdateListIntegration_configurationFromDiscriminatorValue, createUpdateRelationshipFromDiscriminatorValue, createUpdateRelationship_metadataFromDiscriminatorValue, createUpdateResourceFromDiscriminatorValue, createUpdateResource_valuesFromDiscriminatorValue, createValidationRuleFromDiscriminatorValue, createValidationRuleMember1FromDiscriminatorValue, createValidationRuleMember2FromDiscriminatorValue, createViewInputFromDiscriminatorValue, createViewQueryRequestFromDiscriminatorValue, createWorkflowFormFromDiscriminatorValue, createWorkspaceKeywordCountFromDiscriminatorValue, createWorkspaceKeywordCountPageFromDiscriminatorValue, createWorkspaceMemberFromDiscriminatorValue, createWorkspaceRelationshipPageFromDiscriminatorValue, createWorkspaceRelationshipTypeFromDiscriminatorValue, createWorkspaceRelationshipTypePageFromDiscriminatorValue, createWorkspaceRelationshipViewFromDiscriminatorValue, deserializeIntoApplyTemplateInput, deserializeIntoArchiveEntry, deserializeIntoArchiveEntry_values, deserializeIntoArtifact, deserializeIntoArtifact_metrics, deserializeIntoArtifact_sources, deserializeIntoAttachContentTypeInput, deserializeIntoAuditEvent, deserializeIntoAuditEvent_details, deserializeIntoAuditPage, deserializeIntoBlob, deserializeIntoBulkCreate, deserializeIntoBulkCreate_values, deserializeIntoBulkOperation, deserializeIntoBulkOperationMember1, deserializeIntoBulkOperationMember2, deserializeIntoBulkOperationMember3, deserializeIntoBulkOperationMember4, deserializeIntoBulkOperationMember5, deserializeIntoBulkOperationMember6, deserializeIntoBulkOperationMember7, deserializeIntoBulkRequest, deserializeIntoBulkResponse, deserializeIntoBulkResult, deserializeIntoBulkUpdate, deserializeIntoBulkUpdate_values, deserializeIntoCalendarOccurrence, deserializeIntoCalendarQuery, deserializeIntoChunkText, deserializeIntoCollectionContentType, deserializeIntoCollectionTemplate, deserializeIntoCompletionRequest, deserializeIntoCompletionRequest_output_schema, deserializeIntoComposePDF, deserializeIntoContentType, deserializeIntoContentTypeInput, deserializeIntoCreateCatalogField, deserializeIntoCreateField, deserializeIntoCreateListIntegration, deserializeIntoCreateListIntegration_configuration, deserializeIntoCreateRelationship, deserializeIntoCreateRelationship_metadata, deserializeIntoCreateResource, deserializeIntoCreateResource_values, deserializeIntoDecisionRequest, deserializeIntoDecisionRequest_context, deserializeIntoDecisionRequest_data, deserializeIntoDecisionRequest_questions, deserializeIntoDecisionRequest_questions_options, deserializeIntoDeletedResource, deserializeIntoDeltaPage, deserializeIntoDeltaPage_value, deserializeIntoDeltaPage_valueMember1, deserializeIntoDeltaPage_valueMember1_Removed, deserializeIntoDocumentMetadata, deserializeIntoDocumentMetadata_values, deserializeIntoDocumentUpload, deserializeIntoDuplicateDocument, deserializeIntoFieldDefinition, deserializeIntoFieldOptions, deserializeIntoFileVersion, deserializeIntoFilterExpr, deserializeIntoFilteredBulkUpdate, deserializeIntoFilteredBulkUpdate_values, deserializeIntoGenerationChunk, deserializeIntoGenerationChunkPage, deserializeIntoGenerationEnrichments, deserializeIntoGenerationEnrichments_enrichments, deserializeIntoItemIntegration, deserializeIntoItemRevision, deserializeIntoItemRevision_payload, deserializeIntoItemText, deserializeIntoItemText_pages, deserializeIntoKeywordCount, deserializeIntoKeywordCountList, deserializeIntoKeywordInput, deserializeIntoListIntegration, deserializeIntoListView, deserializeIntoListViewPage, deserializeIntoLocatedItem, deserializeIntoMergeTerm, deserializeIntoMoveItem, deserializeIntoMoveTerm, deserializeIntoOperation, deserializeIntoOperationFailure, deserializeIntoOperationPage, deserializeIntoOptimizeImage, deserializeIntoPermissions, deserializeIntoPermissions_effective_grants, deserializeIntoPermissions_grants, deserializeIntoPortableSmartFolder, deserializeIntoPortableSmartFolderDefinition, deserializeIntoPortableTerm, deserializeIntoPortableTermGroup, deserializeIntoPortableTermSet, deserializeIntoPortableTerm_labels, deserializeIntoProblem, deserializeIntoProblem_existing_item, deserializeIntoPromoteKeyword, deserializeIntoPromoteStagedFile, deserializeIntoPromotedFile, deserializeIntoPublication, deserializeIntoPublication_snapshot, deserializeIntoQueryGroup, deserializeIntoQueryGroupPage, deserializeIntoQueryRequest, deserializeIntoQueryResult, deserializeIntoQuerySpec, deserializeIntoReadText, deserializeIntoReclaimFileVersion, deserializeIntoReclaimedFileVersion, deserializeIntoRecycleBinPage, deserializeIntoRelationship, deserializeIntoRelationshipEndpoint, deserializeIntoRelationshipEndpointPage, deserializeIntoRelationshipNeighborhood, deserializeIntoRelationshipType, deserializeIntoRelationshipTypeInput, deserializeIntoRelationshipTypePage, deserializeIntoRelationshipType_attributes, deserializeIntoRelationshipView, deserializeIntoRelationship_metadata, deserializeIntoResource, deserializeIntoResourceAccess, deserializeIntoResource_values, deserializeIntoSchemaRevision, deserializeIntoSchemaRevision_definition, deserializeIntoSchemaRevision_definition_fields, deserializeIntoSchemaTemplate, deserializeIntoSchemaTemplatePage, deserializeIntoSchemaTemplate_definition, deserializeIntoSearchFacetValue, deserializeIntoSearchFacets, deserializeIntoSearchField, deserializeIntoSearchHit, deserializeIntoSearchPage, deserializeIntoSearchRequest, deserializeIntoSearchStatus, deserializeIntoSearchStatus_embeddings, deserializeIntoSearchSync, deserializeIntoSetWorkspaceMember, deserializeIntoSmartFolder, deserializeIntoSmartFolderDefinition, deserializeIntoSmartFolderDrop, deserializeIntoSmartFolderEntry, deserializeIntoSmartFolderGroup, deserializeIntoSmartFolderGroupBy, deserializeIntoSmartFolderGroupsResult, deserializeIntoSmartFolderImportResult, deserializeIntoSmartFolderInput, deserializeIntoSmartFolderPackage, deserializeIntoSmartFolderPage, deserializeIntoSmartFolderPreview, deserializeIntoSmartFolderQueryRequest, deserializeIntoSmartFolderQueryRequest_sort, deserializeIntoSmartFolderResult, deserializeIntoSmartFolderTermRef, deserializeIntoSmartFolderUnclassify, deserializeIntoSortSpec, deserializeIntoStagedArtifact, deserializeIntoStagedFile, deserializeIntoStagedFile_metrics, deserializeIntoStagedFile_sources, deserializeIntoTask, deserializeIntoTaskAnswer, deserializeIntoTaskAnswer_input, deserializeIntoTaskCreate, deserializeIntoTaskCreate_review, deserializeIntoTaskCreate_values, deserializeIntoTaskList, deserializeIntoTaskOutcome, deserializeIntoTaskReassign, deserializeIntoTaskResponseInput, deserializeIntoTaskResponseInput_input, deserializeIntoTask_input, deserializeIntoTask_review, deserializeIntoTask_values, deserializeIntoTaxonomyDecisionRequest, deserializeIntoTaxonomyDecisionRequest_context, deserializeIntoTaxonomyDecisionRequest_data, deserializeIntoTaxonomyDecisionRequest_description_overrides, deserializeIntoTaxonomyImportResult, deserializeIntoTaxonomyPackage, deserializeIntoTemplateField, deserializeIntoTemplateInput, deserializeIntoTerm, deserializeIntoTermEntries, deserializeIntoTermGroup, deserializeIntoTermGroupInput, deserializeIntoTermGroupPage, deserializeIntoTermImportResult, deserializeIntoTermInput, deserializeIntoTermInput_labels, deserializeIntoTermList, deserializeIntoTermPage, deserializeIntoTermSet, deserializeIntoTermSetInput, deserializeIntoTermSetPage, deserializeIntoTerm_labels, deserializeIntoUpdateField, deserializeIntoUpdateListIntegration, deserializeIntoUpdateListIntegration_configuration, deserializeIntoUpdateRelationship, deserializeIntoUpdateRelationship_metadata, deserializeIntoUpdateResource, deserializeIntoUpdateResource_values, deserializeIntoValidationRule, deserializeIntoValidationRuleMember1, deserializeIntoValidationRuleMember2, deserializeIntoViewInput, deserializeIntoViewQueryRequest, deserializeIntoWorkflowForm, deserializeIntoWorkspaceKeywordCount, deserializeIntoWorkspaceKeywordCountPage, deserializeIntoWorkspaceMember, deserializeIntoWorkspaceRelationshipPage, deserializeIntoWorkspaceRelationshipType, deserializeIntoWorkspaceRelationshipTypePage, deserializeIntoWorkspaceRelationshipView, serializeApplyTemplateInput, serializeArchiveEntry, serializeArchiveEntry_values, serializeArtifact, serializeArtifact_metrics, serializeArtifact_sources, serializeAttachContentTypeInput, serializeAuditEvent, serializeAuditEvent_details, serializeAuditPage, serializeBlob, serializeBulkCreate, serializeBulkCreate_values, serializeBulkOperation, serializeBulkOperationMember1, serializeBulkOperationMember2, serializeBulkOperationMember3, serializeBulkOperationMember4, serializeBulkOperationMember5, serializeBulkOperationMember6, serializeBulkOperationMember7, serializeBulkRequest, serializeBulkResponse, serializeBulkResult, serializeBulkUpdate, serializeBulkUpdate_values, serializeCalendarOccurrence, serializeCalendarQuery, serializeChunkText, serializeCollectionContentType, serializeCollectionTemplate, serializeCompletionRequest, serializeCompletionRequest_output_schema, serializeComposePDF, serializeContentType, serializeContentTypeInput, serializeCreateCatalogField, serializeCreateField, serializeCreateListIntegration, serializeCreateListIntegration_configuration, serializeCreateRelationship, serializeCreateRelationship_metadata, serializeCreateResource, serializeCreateResource_values, serializeDecisionRequest, serializeDecisionRequest_context, serializeDecisionRequest_data, serializeDecisionRequest_questions, serializeDecisionRequest_questions_options, serializeDeletedResource, serializeDeltaPage, serializeDeltaPage_value, serializeDeltaPage_valueMember1, serializeDeltaPage_valueMember1_Removed, serializeDocumentMetadata, serializeDocumentMetadata_values, serializeDocumentUpload, serializeDuplicateDocument, serializeFieldDefinition, serializeFieldOptions, serializeFileVersion, serializeFilterExpr, serializeFilteredBulkUpdate, serializeFilteredBulkUpdate_values, serializeGenerationChunk, serializeGenerationChunkPage, serializeGenerationEnrichments, serializeGenerationEnrichments_enrichments, serializeItemIntegration, serializeItemRevision, serializeItemRevision_payload, serializeItemText, serializeItemText_pages, serializeKeywordCount, serializeKeywordCountList, serializeKeywordInput, serializeListIntegration, serializeListView, serializeListViewPage, serializeLocatedItem, serializeMergeTerm, serializeMoveItem, serializeMoveTerm, serializeOperation, serializeOperationFailure, serializeOperationPage, serializeOptimizeImage, serializePermissions, serializePermissions_effective_grants, serializePermissions_grants, serializePortableSmartFolder, serializePortableSmartFolderDefinition, serializePortableTerm, serializePortableTermGroup, serializePortableTermSet, serializePortableTerm_labels, serializeProblem, serializeProblem_existing_item, serializePromoteKeyword, serializePromoteStagedFile, serializePromotedFile, serializePublication, serializePublication_snapshot, serializeQueryGroup, serializeQueryGroupPage, serializeQueryRequest, serializeQueryResult, serializeQuerySpec, serializeReadText, serializeReclaimFileVersion, serializeReclaimedFileVersion, serializeRecycleBinPage, serializeRelationship, serializeRelationshipEndpoint, serializeRelationshipEndpointPage, serializeRelationshipNeighborhood, serializeRelationshipType, serializeRelationshipTypeInput, serializeRelationshipTypePage, serializeRelationshipType_attributes, serializeRelationshipView, serializeRelationship_metadata, serializeResource, serializeResourceAccess, serializeResource_values, serializeSchemaRevision, serializeSchemaRevision_definition, serializeSchemaRevision_definition_fields, serializeSchemaTemplate, serializeSchemaTemplatePage, serializeSchemaTemplate_definition, serializeSearchFacetValue, serializeSearchFacets, serializeSearchField, serializeSearchHit, serializeSearchPage, serializeSearchRequest, serializeSearchStatus, serializeSearchStatus_embeddings, serializeSearchSync, serializeSetWorkspaceMember, serializeSmartFolder, serializeSmartFolderDefinition, serializeSmartFolderDrop, serializeSmartFolderEntry, serializeSmartFolderGroup, serializeSmartFolderGroupBy, serializeSmartFolderGroupsResult, serializeSmartFolderImportResult, serializeSmartFolderInput, serializeSmartFolderPackage, serializeSmartFolderPage, serializeSmartFolderPreview, serializeSmartFolderQueryRequest, serializeSmartFolderQueryRequest_sort, serializeSmartFolderResult, serializeSmartFolderTermRef, serializeSmartFolderUnclassify, serializeSortSpec, serializeStagedArtifact, serializeStagedFile, serializeStagedFile_metrics, serializeStagedFile_sources, serializeTask, serializeTaskAnswer, serializeTaskAnswer_input, serializeTaskCreate, serializeTaskCreate_review, serializeTaskCreate_values, serializeTaskList, serializeTaskOutcome, serializeTaskReassign, serializeTaskResponseInput, serializeTaskResponseInput_input, serializeTask_input, serializeTask_review, serializeTask_values, serializeTaxonomyDecisionRequest, serializeTaxonomyDecisionRequest_context, serializeTaxonomyDecisionRequest_data, serializeTaxonomyDecisionRequest_description_overrides, serializeTaxonomyImportResult, serializeTaxonomyPackage, serializeTemplateField, serializeTemplateInput, serializeTerm, serializeTermEntries, serializeTermGroup, serializeTermGroupInput, serializeTermGroupPage, serializeTermImportResult, serializeTermInput, serializeTermInput_labels, serializeTermList, serializeTermPage, serializeTermSet, serializeTermSetInput, serializeTermSetPage, serializeTerm_labels, serializeUpdateField, serializeUpdateListIntegration, serializeUpdateListIntegration_configuration, serializeUpdateRelationship, serializeUpdateRelationship_metadata, serializeUpdateResource, serializeUpdateResource_values, serializeValidationRule, serializeValidationRuleMember1, serializeValidationRuleMember2, serializeViewInput, serializeViewQueryRequest, serializeWorkflowForm, serializeWorkspaceKeywordCount, serializeWorkspaceKeywordCountPage, serializeWorkspaceMember, serializeWorkspaceRelationshipPage, serializeWorkspaceRelationshipType, serializeWorkspaceRelationshipTypePage, serializeWorkspaceRelationshipView };
}

export {
	models,
};

export {};

declare global {
  // The globals the workflow runtime provides besides ECMAScript itself.
  // dist/workflow.d.ts carries them for the web app's script editor.

  class TextEncoder {
    encode(input?: string): Uint8Array;
  }

  class TextDecoder {
    decode(input?: ArrayBuffer | ArrayBufferView): string;
  }

  // console writes to the run's log, like ctx.log.
  var console: {
    log(...values: unknown[]): void;
    info(...values: unknown[]): void;
    debug(...values: unknown[]): void;
    warn(...values: unknown[]): void;
    error(...values: unknown[]): void;
  };

  var crypto: {
    randomUUID(): string;
  };
}
