(ns demo.store
  (:require [clojure.set :as set]))

#?(:clj
   (defn now [] (System/currentTimeMillis)))

#?(:cljs
   (defn now [] (.now js/Date)))

(defonce ^:private state (atom {}))

(defmacro with-state
  "Runs body with the store state bound."
  [& body]
  `(let [s# @state] ~@body))

(defn- ^:deprecated old-name [] nil)

(defn save!
  [k v]
  (swap! state assoc k v)
  (let [snapshot @state]
    (count snapshot)))

(def ^:const max-items 100)

(defmulti render (fn [item] (:type item)))

(defmethod render :text [item]
  (:body item))

(defmethod render :default [_] "")

(defprotocol Persistent
  (flush! [this])
  (reload! [this path]))

(defrecord FileStore [root]
  Persistent
  (flush! [this] nil)
  (reload! [this path] nil))

(def handlers
  {:save (fn [x] x)
   :load (fn [x] x)})

(comment
  (save! :a 1)
  (def scratch 1))
