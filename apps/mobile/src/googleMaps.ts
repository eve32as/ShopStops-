export type RouteCoordinate = {
  latitude: number;
  longitude: number;
};

export type TrafficETA = {
  fromSequence: number;
  toSequence: number;
  distanceMeters: number;
  durationSeconds: number;
  staticDurationSeconds: number;
  path: RouteCoordinate[];
};

function decodePolyline(encoded: string): RouteCoordinate[] {
  if (encoded.length > 100_000) throw new Error("Google Routes returned an oversized route path.");
  const coordinates: RouteCoordinate[] = [];
  let index = 0;
  let latitude = 0;
  let longitude = 0;
  const decodeValue = () => {
    let result = 0;
    let shift = 0;
    let byte: number;
    do {
      if (index >= encoded.length || shift > 30) {
        throw new Error("Google Routes returned an invalid route path.");
      }
      byte = encoded.charCodeAt(index++) - 63;
      if (byte < 0 || byte > 63) throw new Error("Google Routes returned an invalid route path.");
      result |= (byte & 0x1f) << shift;
      shift += 5;
    } while (byte >= 0x20);
    return (result & 1) ? ~(result >> 1) : (result >> 1);
  };

  while (index < encoded.length) {
    latitude += decodeValue();
    longitude += decodeValue();
    const coordinate = { latitude: latitude / 1e5, longitude: longitude / 1e5 };
    if (
      coordinate.latitude < -90 || coordinate.latitude > 90 ||
      coordinate.longitude < -180 || coordinate.longitude > 180
    ) {
      throw new Error("Google Routes returned out-of-range route coordinates.");
    }
    coordinates.push(coordinate);
  }
  return coordinates;
}

function parseDurationSeconds(value: string) {
  const match = /^(\d+(?:\.\d+)?)s$/.exec(value);
  if (!match) throw new Error("Google Routes returned an invalid duration.");
  const seconds = Math.round(Number(match[1]));
  if (!Number.isSafeInteger(seconds)) {
    throw new Error("Google Routes returned an out-of-range duration.");
  }
  return seconds;
}

export async function getTrafficAwareETAs(
  apiKey: string,
  coordinates: RouteCoordinate[],
  signal: AbortSignal,
): Promise<TrafficETA[]> {
  if (coordinates.length < 2 || coordinates.length > 25 ||
    coordinates.some(({ latitude, longitude }) =>
      !Number.isFinite(latitude) || latitude < -90 || latitude > 90 ||
      !Number.isFinite(longitude) || longitude < -180 || longitude > 180,
    )) {
    throw new Error("Route coordinates are invalid for an ETA request.");
  }
  const waypoint = ({ latitude, longitude }: RouteCoordinate) => ({
    location: { latLng: { latitude, longitude } },
  });
  const response = await fetch("https://routes.googleapis.com/directions/v2:computeRoutes", {
    method: "POST",
    signal,
    headers: {
      "Content-Type": "application/json",
      "X-Goog-Api-Key": apiKey,
      "X-Goog-FieldMask": "routes.legs.duration,routes.legs.staticDuration,routes.legs.distanceMeters,routes.legs.polyline.encodedPolyline",
    },
    body: JSON.stringify({
      origin: waypoint(coordinates[0]),
      destination: waypoint(coordinates[coordinates.length - 1]),
      intermediates: coordinates.slice(1, -1).map(waypoint),
      travelMode: "DRIVE",
      routingPreference: "TRAFFIC_AWARE",
      departureTime: new Date().toISOString(),
      computeAlternativeRoutes: false,
    }),
  });
  if (!response.ok) throw new Error(`Google Routes ETA request failed (${response.status}).`);
  const result = await response.json() as {
    routes?: Array<{
      legs?: Array<{
        duration?: string;
        staticDuration?: string;
        distanceMeters?: number;
        polyline?: { encodedPolyline?: string };
      }>;
    }>;
  };
  const legs = result.routes?.[0]?.legs ?? [];
  if (legs.length !== coordinates.length - 1) {
    throw new Error("Google Routes returned an incomplete route ETA.");
  }
  return legs.map((leg, index) => {
    if (typeof leg.duration !== "string" || typeof leg.staticDuration !== "string" ||
      !Number.isSafeInteger(leg.distanceMeters) || (leg.distanceMeters ?? -1) < 0) {
      throw new Error("Google Routes returned invalid route ETA details.");
    }
    return {
      fromSequence: index + 1,
      toSequence: index + 2,
      distanceMeters: leg.distanceMeters!,
      durationSeconds: parseDurationSeconds(leg.duration),
      staticDurationSeconds: parseDurationSeconds(leg.staticDuration),
      path: typeof leg.polyline?.encodedPolyline === "string"
        ? decodePolyline(leg.polyline.encodedPolyline)
        : [],
    };
  });
}
