import { useEffect, useRef } from "react";
import * as THREE from "three";
import { hasCoords } from "./geo";
import { MapLegend } from "./MapLegend";
import type { MapSession } from "./types";

export default function GlobeView({ sessions }: { sessions: MapSession[] }) {
  const mountRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const mount = mountRef.current;
    if (!mount) return undefined;

    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(35, 1, 0.1, 100);
    camera.position.set(0, 0, 4.2);

    const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, preserveDrawingBuffer: true });
    renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
    mount.appendChild(renderer.domElement);

    const globe = new THREE.Group();
    scene.add(globe);

    const sphere = new THREE.Mesh(
      new THREE.SphereGeometry(1.35, 96, 48),
      new THREE.MeshStandardMaterial({
        color: 0x123d51,
        roughness: 0.78,
        metalness: 0.05,
        emissive: 0x03161e,
      }),
    );
    globe.add(sphere);

    const wire = new THREE.Mesh(
      new THREE.SphereGeometry(1.358, 32, 16),
      new THREE.MeshBasicMaterial({ color: 0x77d5ff, wireframe: true, transparent: true, opacity: 0.12 }),
    );
    globe.add(wire);

    const atmosphere = new THREE.Mesh(
      new THREE.SphereGeometry(1.42, 96, 48),
      new THREE.MeshBasicMaterial({ color: 0x5eead4, transparent: true, opacity: 0.08, side: THREE.BackSide }),
    );
    globe.add(atmosphere);

    const light = new THREE.DirectionalLight(0xffffff, 3);
    light.position.set(2, 3, 4);
    scene.add(light);
    scene.add(new THREE.AmbientLight(0x7dd3fc, 1.1));

    const routeMaterial = new THREE.LineBasicMaterial({ color: 0x95f6ff, transparent: true, opacity: 0.55 });
    sessions.forEach((session) => {
      const clientPos = latLonToVector(session.client.lat!, session.client.lon!, 1.42);
      const marker = new THREE.Mesh(
        new THREE.SphereGeometry(0.032, 16, 16),
        new THREE.MeshBasicMaterial({ color: session.cdn ? 0xf9c74f : 0x2dd4bf }),
      );
      marker.position.copy(clientPos);
      globe.add(marker);

      if (session.server && hasCoords(session.server)) {
        const serverPos = latLonToVector(session.server.lat!, session.server.lon!, 1.41);
        const curve = new THREE.QuadraticBezierCurve3(
          serverPos,
          serverPos.clone().add(clientPos).normalize().multiplyScalar(1.85),
          clientPos,
        );
        const route = new THREE.Line(new THREE.BufferGeometry().setFromPoints(curve.getPoints(36)), routeMaterial);
        globe.add(route);
      }
    });

    const resize = () => {
      const rect = mount.getBoundingClientRect();
      const width = Math.max(320, rect.width);
      const height = Math.max(360, rect.height);
      renderer.setSize(width, height, false);
      camera.aspect = width / height;
      camera.updateProjectionMatrix();
    };
    resize();
    const observer = new ResizeObserver(resize);
    observer.observe(mount);

    let frame = 0;
    const animate = () => {
      frame = window.requestAnimationFrame(animate);
      globe.rotation.y += 0.0022;
      renderer.render(scene, camera);
    };
    animate();

    return () => {
      window.cancelAnimationFrame(frame);
      observer.disconnect();
      renderer.dispose();
      mount.removeChild(renderer.domElement);
    };
  }, [sessions]);

  return (
    <div className="globe-shell">
      <div className="globe-canvas" ref={mountRef} />
      <MapLegend sessions={sessions} />
    </div>
  );
}

function latLonToVector(lat: number, lon: number, radius: number) {
  const phi = (90 - lat) * (Math.PI / 180);
  const theta = (lon + 180) * (Math.PI / 180);
  return new THREE.Vector3(
    -radius * Math.sin(phi) * Math.cos(theta),
    radius * Math.cos(phi),
    radius * Math.sin(phi) * Math.sin(theta),
  );
}
