// @generated by protoc-gen-es v1.3.0 with parameter "target=dts"
// @generated from file zetachain/zetacore/authority/query.proto (package zetachain.zetacore.authority, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";
import type { Policies } from "./policies_pb.js";
import type { ChainInfo } from "./chain_info_pb.js";

/**
 * QueryGetPoliciesRequest is the request type for the Query/Policies RPC
 * method.
 *
 * @generated from message zetachain.zetacore.authority.QueryGetPoliciesRequest
 */
export declare class QueryGetPoliciesRequest extends Message<QueryGetPoliciesRequest> {
  constructor(data?: PartialMessage<QueryGetPoliciesRequest>);

  static readonly runtime: typeof proto3;
  static readonly typeName = "zetachain.zetacore.authority.QueryGetPoliciesRequest";
  static readonly fields: FieldList;

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): QueryGetPoliciesRequest;

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): QueryGetPoliciesRequest;

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): QueryGetPoliciesRequest;

  static equals(a: QueryGetPoliciesRequest | PlainMessage<QueryGetPoliciesRequest> | undefined, b: QueryGetPoliciesRequest | PlainMessage<QueryGetPoliciesRequest> | undefined): boolean;
}

/**
 * QueryGetPoliciesResponse is the response type for the Query/Policies RPC
 * method.
 *
 * @generated from message zetachain.zetacore.authority.QueryGetPoliciesResponse
 */
export declare class QueryGetPoliciesResponse extends Message<QueryGetPoliciesResponse> {
  /**
   * @generated from field: zetachain.zetacore.authority.Policies policies = 1;
   */
  policies?: Policies;

  constructor(data?: PartialMessage<QueryGetPoliciesResponse>);

  static readonly runtime: typeof proto3;
  static readonly typeName = "zetachain.zetacore.authority.QueryGetPoliciesResponse";
  static readonly fields: FieldList;

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): QueryGetPoliciesResponse;

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): QueryGetPoliciesResponse;

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): QueryGetPoliciesResponse;

  static equals(a: QueryGetPoliciesResponse | PlainMessage<QueryGetPoliciesResponse> | undefined, b: QueryGetPoliciesResponse | PlainMessage<QueryGetPoliciesResponse> | undefined): boolean;
}

/**
 * QueryGetChainInfoRequest is the request type for the Query/ChainInfo RPC
 * method.
 *
 * @generated from message zetachain.zetacore.authority.QueryGetChainInfoRequest
 */
export declare class QueryGetChainInfoRequest extends Message<QueryGetChainInfoRequest> {
  constructor(data?: PartialMessage<QueryGetChainInfoRequest>);

  static readonly runtime: typeof proto3;
  static readonly typeName = "zetachain.zetacore.authority.QueryGetChainInfoRequest";
  static readonly fields: FieldList;

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): QueryGetChainInfoRequest;

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): QueryGetChainInfoRequest;

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): QueryGetChainInfoRequest;

  static equals(a: QueryGetChainInfoRequest | PlainMessage<QueryGetChainInfoRequest> | undefined, b: QueryGetChainInfoRequest | PlainMessage<QueryGetChainInfoRequest> | undefined): boolean;
}

/**
 * QueryGetChainInfoResponse is the response type for the Query/ChainInfo RPC
 * method.
 *
 * @generated from message zetachain.zetacore.authority.QueryGetChainInfoResponse
 */
export declare class QueryGetChainInfoResponse extends Message<QueryGetChainInfoResponse> {
  /**
   * @generated from field: zetachain.zetacore.authority.ChainInfo chain_info = 1;
   */
  chainInfo?: ChainInfo;

  constructor(data?: PartialMessage<QueryGetChainInfoResponse>);

  static readonly runtime: typeof proto3;
  static readonly typeName = "zetachain.zetacore.authority.QueryGetChainInfoResponse";
  static readonly fields: FieldList;

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): QueryGetChainInfoResponse;

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): QueryGetChainInfoResponse;

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): QueryGetChainInfoResponse;

  static equals(a: QueryGetChainInfoResponse | PlainMessage<QueryGetChainInfoResponse> | undefined, b: QueryGetChainInfoResponse | PlainMessage<QueryGetChainInfoResponse> | undefined): boolean;
}

