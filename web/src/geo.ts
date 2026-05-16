import type { Endpoint } from "./types";

export function hasCoords(endpoint?: Endpoint): endpoint is Endpoint & { lat: number; lon: number } {
  return typeof endpoint?.lat === "number" && typeof endpoint.lon === "number";
}

export function project(lat: number, lon: number) {
  return { x: ((lon + 180) / 360) * 1000, y: ((90 - lat) / 180) * 500 };
}
