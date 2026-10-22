import { useCallback, useEffect, useMemo, useRef } from "react";
import { StyleSheet, Text, View } from "react-native";
import MapView, { Marker, Polyline, type Region } from "react-native-maps";
import type { RouteCoordinate } from "./googleMaps";

type RouteStop = RouteCoordinate & {
  id: string;
  sequence: number;
  type: string;
  status: string;
};

type Props = {
  stops: RouteStop[];
  path: RouteCoordinate[];
  tracking: boolean;
};

function getInitialRegion(points: RouteCoordinate[]): Region {
  let latitudeMin = points[0].latitude;
  let latitudeMax = points[0].latitude;
  let longitudeMin = points[0].longitude;
  let longitudeMax = points[0].longitude;
  for (const point of points.slice(1)) {
    latitudeMin = Math.min(latitudeMin, point.latitude);
    latitudeMax = Math.max(latitudeMax, point.latitude);
    longitudeMin = Math.min(longitudeMin, point.longitude);
    longitudeMax = Math.max(longitudeMax, point.longitude);
  }
  return {
    latitude: (latitudeMin + latitudeMax) / 2,
    longitude: (longitudeMin + longitudeMax) / 2,
    latitudeDelta: Math.max(0.012, (latitudeMax - latitudeMin) * 1.6),
    longitudeDelta: Math.max(0.012, (longitudeMax - longitudeMin) * 1.6),
  };
}

export function DriverRouteMap({ stops, path, tracking }: Props) {
  const points = useMemo(() => path.length >= 2
    ? path
    : stops.map(({ latitude, longitude }) => ({ latitude, longitude })),
  [path, stops]);
  const initialRegion = useMemo(() => points.length
    ? getInitialRegion(points)
    : { latitude: 0, longitude: 0, latitudeDelta: 0.1, longitudeDelta: 0.1 },
  [points]);
  const mapRef = useRef<MapView>(null);
  const fitRoute = useCallback(() => {
    if (points.length < 2) return;
    mapRef.current?.fitToCoordinates(points, {
      edgePadding: { top: 36, right: 36, bottom: 36, left: 36 },
      animated: true,
    });
  }, [points]);

  useEffect(() => {
    fitRoute();
  }, [fitRoute]);

  if (stops.length === 0 || points.length < 2) {
    return (
      <View style={styles.empty}>
        <Text style={styles.emptyText}>Route map is unavailable until the route has at least two stops.</Text>
      </View>
    );
  }

  return (
    <MapView
      accessibilityLabel="Interactive driver route map"
      initialRegion={initialRegion}
      key={stops.map((stop) => stop.id).join(":")}
      onMapReady={fitRoute}
      ref={mapRef}
      rotateEnabled={false}
      scrollEnabled
      showsCompass
      showsUserLocation={tracking}
      style={styles.map}
      toolbarEnabled
      zoomEnabled
    >
      <Polyline coordinates={points} strokeColor="#e5653d" strokeWidth={5} />
      {stops.map((stop) => (
        <Marker
          coordinate={{ latitude: stop.latitude, longitude: stop.longitude }}
          key={stop.id}
          title={`${stop.type === "dropoff" ? "Drop-off" : "Pickup"} · Stop ${stop.sequence}`}
          description={stop.status.replaceAll("_", " ")}
          pinColor={stop.type === "dropoff" ? "#19352f" : "#e5653d"}
        >
          <View style={[styles.marker, stop.type === "dropoff" && styles.dropoffMarker]}>
            <Text style={styles.markerText}>{stop.sequence}</Text>
          </View>
        </Marker>
      ))}
    </MapView>
  );
}

const styles = StyleSheet.create({
  map: {
    height: 300,
    width: "100%",
    borderRadius: 14,
    marginTop: 14,
  },
  empty: {
    height: 120,
    borderRadius: 14,
    backgroundColor: "#f8f9f7",
    alignItems: "center",
    justifyContent: "center",
    marginTop: 14,
    padding: 18,
  },
  emptyText: { color: "#65756f", fontSize: 12, textAlign: "center" },
  marker: {
    width: 30,
    height: 30,
    borderRadius: 15,
    backgroundColor: "#e5653d",
    borderColor: "#fff",
    borderWidth: 2,
    alignItems: "center",
    justifyContent: "center",
  },
  dropoffMarker: { backgroundColor: "#19352f" },
  markerText: { color: "#fff", fontSize: 12, fontWeight: "800" },
});
