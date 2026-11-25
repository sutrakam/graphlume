(ns demo.core
  "Namespace docstring."
  (:require [clojure.string :as str]))

(def answer 42)

(def ^:private hidden "secret")

(def ^{:doc "documented"} documented 1)

(defonce registry (atom {}))

(defn greet
  "Says hello."
  [name]
  (str "Hello, " name))

(defn- helper [x] (inc x))

(defn ^:private meta-fn [x] x)

(defn ^String typed-fn
  [x]
  (let [local-binding 1
        other (helper x)]
    (def inner-def 5)
    (str local-binding other)))

(defn multi-arity
  ([] (multi-arity 0))
  ([x] (inc x)))

(defmacro unless [test & body]
  `(if (not ~test) (do ~@body)))

(defmulti area :shape)

(defmulti ^String describe class)

(defmethod area :circle [{:keys [r]}]
  (* 3 r r))

(defmethod area :default [_] 0)

(defprotocol Shape
  "A shape."
  (perimeter [this] "Perimeter.")
  (scale [this factor]
    "Scale it.")
  (describe-shape [this]))

(defprotocol ^{:added "1.6"} Streamable
  (write-body [body out]))

(defrecord Circle [radius]
  Shape
  (perimeter [this] (* 2 3.14 radius))
  (scale [this factor] (->Circle (* radius factor))))

(deftype Point [x y]
  Object
  (toString [_] "point"))

(definterface Marker
  (mark []))

#?(:clj (defn clj-only [x] x)
   :cljs (defn cljs-only [x] x))

(comment
  (defn in-comment [] 1))

(extend-protocol Shape
  String
  (perimeter [s] (count s)))

(reify Shape
  (perimeter [_] 0))

(println "side effect" (greet "x"))

^:cljs (def platform-name "browser")

^:clj (defn jvm-only [x] x)

(declare forward-ref)

(deftest greet-test
  (is (= "Hello, a" (greet "a"))))

(defprotocol Store (load-all [s]) (save-all [s items]))

(let [captured 1]
  (defn closure-defined [] captured))
