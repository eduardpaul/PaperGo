// HostParseNode reads parsed JSON into Kiota models with the same rules as
// Kiota's JsonParseNode, whose results it must equal (scripts/test.mjs
// checks every model), at a fraction of the allocations: its methods are
// shared on the prototype instead of made per node, an object's field
// deserializers are made once per type and reused from a pool instead of
// three times per object, and the fields of an object are read through one
// reused child node.

import {
  DateOnly,
  Duration,
  TimeOnly,
  createBackedModelProxyHandler,
  createUntypedArray,
  createUntypedBoolean,
  createUntypedNodeFromDiscriminatorValue,
  createUntypedNull,
  createUntypedNumber,
  createUntypedObject,
  createUntypedString,
  inNodeEnv,
  isBackingStoreEnabled,
  isUntypedNode,
  parseGuidString,
  type BackingStoreFactory,
  type Guid,
  type Parsable,
  type ParsableFactory,
  type ParseNode,
  type PrimitiveTypesForDeserializationForCollection,
  type UntypedNode,
} from "@microsoft/kiota-abstractions";

// Kiota's getEnumValueFromStringValue reverses the enum's record on every
// value; enum records are constant, so each is reversed once.
const reversed = new WeakMap<object, Record<string, string>>();

function enumValue(value: string, type: Record<string, unknown>): unknown {
  let names = reversed.get(type);
  if (names === undefined) {
    names = Object.fromEntries(Object.entries(type).map(([key, v]) => [v, key])) as Record<string, string>;
    reversed.set(type, names);
  }
  return type[names[value]];
}

// Deserializers make a record of closures for the object they fill. Slots
// keep such records with the objects they fill, by deserializer, for reuse;
// a slot is out of its pool while it reads, so nested objects of one type
// take their own.
interface Slot {
  target: Record<string, unknown>;
  fields: Record<string, (node: ParseNode) => void>;
}
// null marks deserializers of untyped and backed models, which are not pooled.
const pools = new WeakMap<object, Slot[] | null>();

export class HostParseNode implements ParseNode {
  onBeforeAssignFieldValues: ((value: Parsable) => void) | undefined;
  onAfterAssignFieldValues: ((value: Parsable) => void) | undefined;
  // child reads this node's fields and items, one value at a time.
  private child: HostParseNode | undefined;

  constructor(
    private value: unknown,
    private readonly store: BackingStoreFactory | undefined,
  ) {}

  // at points the reused child at value.
  private at(value: unknown): HostParseNode {
    this.child ??= new HostParseNode(undefined, this.store);
    this.child.value = value;
    return this.child;
  }

  getStringValue(): string | undefined {
    return stringOf(this.value);
  }

  getChildNode(identifier: string): ParseNode | undefined {
    const value = this.value;
    if (value && typeof value === "object" && (value as Record<string, unknown>)[identifier] !== undefined) {
      return new HostParseNode((value as Record<string, unknown>)[identifier], this.store);
    }
    return undefined;
  }

  getBooleanValue(): boolean | undefined {
    return typeof this.value === "boolean" ? this.value : undefined;
  }

  getNumberValue(): number | undefined {
    return typeof this.value === "number" ? this.value : undefined;
  }

  getGuidValue(): Guid | undefined {
    return parseGuidString(stringOf(this.value));
  }

  getDateValue(): Date | undefined {
    return dateOf(this.value);
  }

  getDateOnlyValue(): DateOnly | undefined {
    return dateOnlyOf(this.value);
  }

  getTimeOnlyValue(): TimeOnly | undefined {
    return timeOnlyOf(this.value);
  }

  getDurationValue(): Duration | undefined {
    return durationOf(this.value);
  }

  getCollectionOfPrimitiveValues<T>(type: PrimitiveTypesForDeserializationForCollection): T[] | undefined {
    if (!Array.isArray(this.value)) return undefined;
    return this.value.map((x) => primitiveOf(x, type) as T);
  }

  getCollectionOfObjectValues<T extends Parsable>(factory: ParsableFactory<T>): T[] | undefined {
    if (!Array.isArray(this.value)) return undefined;
    const out: T[] = [];
    for (const item of this.value) out.push(this.at(item).getObjectValue(factory));
    return out;
  }

  getObjectValue<T extends Parsable>(factory: ParsableFactory<T>): T {
    const deserializer = factory(this);
    if (!this.onBeforeAssignFieldValues && !this.onAfterAssignFieldValues) {
      const pooled = this.pooled(deserializer);
      if (pooled) return pooled as T;
    }
    const temp = {} as T;
    let fields = deserializer(temp);
    if (isUntypedNode(fields)) return this.untyped() as unknown as T;
    let model = temp;
    if (isBackingStoreEnabled(fields) && this.store) {
      // The deserializers assign to the object they are made for.
      model = new Proxy(temp, createBackedModelProxyHandler(this.store));
      fields = factory(this)(model);
    }
    this.onBeforeAssignFieldValues?.(model);
    const value = this.value;
    if (value) {
      for (const key of Object.keys(value as object)) {
        const v = (value as Record<string, unknown>)[key];
        const deserialize = fields[key];
        if (deserialize) {
          deserialize(this.at(v));
        } else {
          const holder = model as { additionalData?: Record<string, unknown> };
          holder.additionalData ??= {};
          holder.additionalData[key] = v;
        }
      }
    }
    this.onAfterAssignFieldValues?.(model);
    return model;
  }

  // pooled reads the node with deserializer's fields from its pool, or
  // returns undefined for models it does not pool: untyped and backed ones.
  // The fields assign to their slot's target; the values then move to the
  // model in the order they were assigned, and the slot goes back.
  private pooled(deserializer: (instance?: Parsable) => Record<string, (node: ParseNode) => void>): Parsable | undefined {
    let pool = pools.get(deserializer);
    if (pool === null) return undefined;
    if (!pool) pools.set(deserializer, (pool = []));
    let slot = pool.pop();
    if (!slot) {
      const target: Record<string, unknown> = {};
      const fields = deserializer(target as Parsable);
      if (isUntypedNode(fields) || isBackingStoreEnabled(fields)) {
        pools.set(deserializer, null);
        return undefined;
      }
      slot = { target, fields };
    }
    const { target, fields } = slot;
    const value = this.value;
    if (value) {
      for (const key of Object.keys(value as object)) {
        const v = (value as Record<string, unknown>)[key];
        const deserialize = fields[key];
        if (deserialize) {
          deserialize(this.at(v));
        } else {
          const holder = target as { additionalData?: Record<string, unknown> };
          holder.additionalData ??= {};
          holder.additionalData[key] = v;
        }
      }
    }
    const model: Record<string, unknown> = {};
    for (const key of Object.keys(target)) {
      model[key] = target[key];
      delete target[key];
    }
    pool.push(slot);
    return model as Parsable;
  }

  // untyped reads the node as Kiota's untyped values, as JsonParseNode does.
  private untyped(): UntypedNode {
    const value = this.value;
    if (typeof value === "boolean") return createUntypedBoolean(value);
    if (typeof value === "string") return createUntypedString(value);
    if (typeof value === "number") return createUntypedNumber(value);
    if (Array.isArray(value)) {
      return createUntypedArray(value.map((x) => new HostParseNode(x, this.store).getObjectValue(createUntypedNodeFromDiscriminatorValue)));
    }
    if (value && typeof value === "object") {
      const properties: Record<string, UntypedNode> = {};
      for (const key of Object.keys(value)) {
        properties[key] = new HostParseNode((value as Record<string, unknown>)[key], this.store).getObjectValue(createUntypedNodeFromDiscriminatorValue);
      }
      return createUntypedObject(properties);
    }
    if (!value) return createUntypedNull();
    return {} as UntypedNode;
  }

  getCollectionOfEnumValues<T>(type: any): T[] {
    if (!Array.isArray(this.value)) return [];
    const out: T[] = [];
    for (const x of this.value) {
      const v = typeof x === "string" ? enumValue(x, type) : undefined;
      if (v !== undefined) out.push(v as T);
    }
    return out;
  }

  getEnumValue<T>(type: any): T | undefined {
    const raw = stringOf(this.value);
    if (!raw) return undefined;
    return enumValue(raw, type) as T | undefined;
  }

  getByteArrayValue(): ArrayBuffer | undefined {
    const s = stringOf(this.value);
    if (!s || s.length === 0) return undefined;
    return inNodeEnv() ? new Uint8Array(Buffer.from(s, "base64")).buffer : (new TextEncoder().encode(s).buffer as ArrayBuffer);
  }
}

declare const Buffer: { from(s: string, encoding: string): Uint8Array };

function stringOf(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function dateOf(value: unknown): Date | undefined {
  if (value instanceof Date) return new Date(value.getTime());
  if (typeof value === "number" || typeof value === "string") return new Date(value);
  return undefined;
}

function dateOnlyOf(value: unknown): DateOnly | undefined {
  return value instanceof DateOnly ? value : DateOnly.parse(stringOf(value));
}

function timeOnlyOf(value: unknown): TimeOnly | undefined {
  return value instanceof TimeOnly ? value : TimeOnly.parse(stringOf(value));
}

function durationOf(value: unknown): Duration | undefined {
  return value instanceof Duration ? value : Duration.parse(stringOf(value));
}

function primitiveOf(value: unknown, type: PrimitiveTypesForDeserializationForCollection): unknown {
  if (value === null) return null;
  switch (type) {
    case "boolean":
    case "number":
    case "string":
      if (typeof value !== type) throw new Error(`encountered an unsupported type during deserialization ${typeof value}`);
      return value;
    case "Date":
      return dateOf(value);
    case "DateOnly":
      return dateOnlyOf(value);
    case "TimeOnly":
      return timeOnlyOf(value);
    case "Duration":
      return durationOf(value);
    default:
      throw new Error(`encountered an unsupported type during deserialization ${type}`);
  }
}
