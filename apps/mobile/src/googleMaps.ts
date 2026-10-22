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
};

function parseDurationSeconds(value: string) {
  const match = /^(\d+(?:\.\d+)?)s$/.exec(value);
  if (!match) throw new Error("Google Routes returned an invalid duration.");
  return Math.round(Number(match[1]));
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
      "X-Goog-FieldMask": "routes.legs.duration,routes.legs.staticDuration,routes.legs.distanceMeters",
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
      legs?: Array<{ duration?: string; staticDuration?: string; distanceMeters?: number }>;
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
    };
  });
}
